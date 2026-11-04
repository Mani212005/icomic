"""Redis delivery recovery using an in-process Redis implementation and Lua."""

from concurrent.futures import ThreadPoolExecutor

from fakeredis import FakeRedis

from app.adapters.queue.redis import RedisQueueAdapter
from app.services.worker import QUEUE, GenerationWorker


def test_atomic_concurrent_submission_creates_one_delivery():
    client = FakeRedis()

    def submit(_):
        return RedisQueueAdapter(client=client).enqueue(QUEUE, {"items": []}, "same-key")

    with ThreadPoolExecutor(max_workers=8) as executor:
        assert len(set(executor.map(submit, range(32)))) == 1
    assert client.llen("icomic:{generation}:ready") == 1


def test_interrupted_claim_fails_without_repeating_provider_call(monkeypatch):
    now = [1000.0]
    monkeypatch.setattr("app.adapters.queue.redis.time.time", lambda: now[0])
    queue = RedisQueueAdapter(client=FakeRedis(), lease_s=10)
    queue.enqueue(QUEUE, {"operation": "image", "request": {}}, "key")
    envelope = queue.dequeue(QUEUE)
    assert envelope is not None
    assert queue.get_job(QUEUE, "key")["status"] == "running"
    # Simulate process death before saving a provider response.
    now[0] += 11
    worker = GenerationWorker(RedisQueueAdapter(client=queue.client))
    worker.register("image", lambda request: (_ for _ in ()).throw(AssertionError("repeated call")))
    assert worker.run_once() is False
    saved = queue.get_job(QUEUE, "key")
    assert saved["status"] == "failed"
    assert "outcome is unknown" in saved["error_message"]
    # A stale consumer cannot overwrite the terminal state.
    import pytest

    with pytest.raises(RuntimeError, match="no longer owns"):
        queue.finish(envelope, {"status": "done", "response": {}})


def test_redelivery_after_saved_result_does_not_generate():
    queue = RedisQueueAdapter(client=FakeRedis())
    queue.enqueue(QUEUE, {"operation": "image", "request": {}}, "key")
    serialized = queue.client.lindex("icomic:{generation}:ready", 0)
    envelope = queue.dequeue(QUEUE)
    queue.finish(envelope, {"status": "done", "response": {"image_url": "saved"}})
    queue.client.rpush("icomic:{generation}:ready", serialized)
    worker = GenerationWorker(queue)
    worker.register("image", lambda request: (_ for _ in ()).throw(AssertionError("repeated call")))
    assert worker.run_once()
    assert queue.get_job(QUEUE, "key")["response"] == {"image_url": "saved"}


def test_duplicate_running_delivery_does_not_generate_concurrently():
    queue = RedisQueueAdapter(client=FakeRedis())
    queue.enqueue(QUEUE, {"operation": "image", "request": {}}, "key")
    serialized = queue.client.lindex("icomic:{generation}:ready", 0)
    owner = queue.dequeue(QUEUE)
    queue.client.rpush("icomic:{generation}:ready", serialized)
    worker = GenerationWorker(queue)
    worker.register("image", lambda request: (_ for _ in ()).throw(AssertionError("repeated call")))
    assert worker.run_once()
    queue.finish(owner, {"status": "done", "response": {"image_url": "saved"}})
    assert queue.get_job(QUEUE, "key")["status"] == "done"
