"""Redis delivery recovery using an in-process Redis implementation and Lua."""

from concurrent.futures import ThreadPoolExecutor

from fakeredis import FakeRedis

from app.adapters.meta_http import MetaContentPolicyError, MetaRateLimitError
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
    assert saved["status"] == "uncertain"
    assert "outcome is unknown" in saved["error_message"]
    # A stale consumer cannot overwrite the terminal state.
    import pytest

    with pytest.raises(RuntimeError, match="no longer owns"):
        queue.finish(envelope, {"status": "done", "response": {}})


def test_unknown_provider_outcome_is_saved_as_terminal_uncertain():
    class UnknownOutcome(RuntimeError):
        outcome_unknown = True

    queue = RedisQueueAdapter(client=FakeRedis())
    queue.enqueue(QUEUE, {"operation": "image", "request": {}}, "uncertain-key")
    worker = GenerationWorker(queue)
    worker.register("image", lambda _: (_ for _ in ()).throw(UnknownOutcome("outcome is unknown")))
    assert worker.run_once()
    saved = queue.get_job(QUEUE, "uncertain-key")
    assert saved["status"] == "uncertain"
    assert saved["http_status"] == 502
    assert saved["error_code"] == "OUTCOME_UNKNOWN"
    assert (
        saved["error_message"]
        == "The generation outcome could not be confirmed. Check its status before retrying."
    )
    assert saved["error_message"] != "outcome is unknown"
    assert worker.run_once() is False


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


def test_worker_persists_safe_structured_retryable_error():
    queue = RedisQueueAdapter(client=FakeRedis())
    queue.enqueue(QUEUE, {"operation": "image", "request": {}}, "error-key")
    worker = GenerationWorker(queue)
    provider_error = MetaRateLimitError("private upstream body with token=secret", retry_after=17.5)
    provider_error.actual_cost = 0.07
    worker.register(
        "image",
        lambda request: (_ for _ in ()).throw(provider_error),
    )

    assert worker.run_once()
    saved = queue.get_job(QUEUE, "error-key")

    assert saved["error_code"] == "RATE_LIMITED"
    assert saved["error_message"] == "Generation is temporarily rate limited. Try again later."
    assert saved["retryable"] is True
    assert saved["retry_after_seconds"] == 17.5
    assert saved["actual_cost"] == 0.07
    assert "secret" not in str(saved)
    assert "private upstream" not in str(saved)


def test_worker_persists_policy_revision_guidance_without_provider_text():
    queue = RedisQueueAdapter(client=FakeRedis())
    queue.enqueue(QUEUE, {"operation": "image", "request": {}}, "policy-key")
    worker = GenerationWorker(queue)
    worker.register(
        "image",
        lambda request: (_ for _ in ()).throw(
            MetaContentPolicyError("blocked because private prompt content was unsafe")
        ),
    )

    assert worker.run_once()
    saved = queue.get_job(QUEUE, "policy-key")

    assert saved["error_code"] == "CONTENT_POLICY_REJECTED"
    assert (
        saved["error_message"]
        == "This request could not be completed under content guidelines. Edit it and try again."
    )
    assert saved["retryable"] is False
    assert saved.get("retry_after_seconds") is None
    assert "private prompt" not in str(saved)
