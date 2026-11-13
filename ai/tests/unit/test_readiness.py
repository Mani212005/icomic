"""Readiness differs from liveness: it fails while a dependency cannot do work."""

import pytest
from fastapi.testclient import TestClient

from app.adapters.clock.fake import FakeClockAdapter
from app.adapters.factory import AdapterSet
from app.adapters.image.fake import FakeImageAdapter
from app.adapters.language.fake import FakeLanguageAdapter
from app.adapters.queue.fake import FakeQueueAdapter
from app.adapters.queue.redis import RedisQueueAdapter
from app.adapters.storage.fake import FakeStorageAdapter
from app.adapters.storage.local import LocalStorageAdapter
from app.adapters.vision.fake import FakeVisionAdapter
from app.main import create_app


def _adapters(storage, queue):
    return AdapterSet(
        language=FakeLanguageAdapter(),
        vision=FakeVisionAdapter(),
        image=FakeImageAdapter(),
        storage=storage,
        queue=queue,
        clock=FakeClockAdapter(),
    )


def test_ready_when_storage_and_queue_work(tmp_path):
    with TestClient(create_app(_adapters(LocalStorageAdapter(tmp_path), FakeQueueAdapter()))) as c:
        response = c.get("/readyz")
    assert response.status_code == 200
    assert response.json() == {"status": "ready", "checks": {"storage": "ok", "queue": "ok"}}


def test_fakes_without_a_probe_count_as_ready():
    with TestClient(create_app(_adapters(FakeStorageAdapter(), FakeQueueAdapter()))) as c:
        assert c.get("/readyz").status_code == 200


def test_not_ready_while_the_storage_volume_is_unwritable_but_still_live(tmp_path, monkeypatch):
    storage = LocalStorageAdapter(tmp_path)
    monkeypatch.setattr("app.adapters.storage.local.os.access", lambda *a, **k: False)
    with TestClient(create_app(_adapters(storage, FakeQueueAdapter()))) as c:
        response = c.get("/readyz")
        assert c.get("/healthz").status_code == 200
    assert response.status_code == 503
    assert response.json() == {
        "status": "not_ready",
        "checks": {"storage": "unavailable", "queue": "ok"},
    }


class _DownRedis:
    def ping(self):
        raise ConnectionError("redis://user:secret@redis:6379 refused")


def test_not_ready_while_redis_is_down_and_the_reason_is_not_leaked(tmp_path):
    queue = RedisQueueAdapter(client=_DownRedis())
    adapters = _adapters(LocalStorageAdapter(tmp_path), queue)
    adapters.queue.synchronous = True  # Keep the unstarted worker out of this probe.
    with TestClient(create_app(adapters)) as c:
        response = c.get("/readyz")
    assert response.status_code == 503
    assert response.json()["checks"] == {"storage": "ok", "queue": "unavailable"}
    assert "secret" not in response.text


def test_redis_queue_ready_pings_the_client():
    pings = []

    class Redis:
        def ping(self):
            pings.append(1)

    RedisQueueAdapter(client=Redis()).ready()
    assert pings == [1]
    with pytest.raises(ConnectionError):
        RedisQueueAdapter(client=_DownRedis()).ready()
