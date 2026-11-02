"""Unit tests for port adapters (hermetic, no network, deterministic)."""

from datetime import timedelta, timezone

import pytest
from fastapi.testclient import TestClient

from app.adapters.clock.fake import FakeClockAdapter
from app.adapters.clock.port import ClockPort
from app.adapters.clock.system import SystemClockAdapter
from app.adapters.factory import (
    AdapterSet,
    build_adapters_from_env,
    get_clock_adapter,
    get_image_adapter,
    get_language_adapter,
    get_queue_adapter,
    get_storage_adapter,
    get_vision_adapter,
)
from app.adapters.image.fake import FakeImageAdapter
from app.adapters.image.meta import MetaImageAdapter
from app.adapters.image.port import ImagePort
from app.adapters.language.fake import FakeLanguageAdapter
from app.adapters.language.meta import MetaLanguageAdapter
from app.adapters.language.port import LanguagePort, Message
from app.adapters.queue.fake import FakeQueueAdapter
from app.adapters.queue.port import QueuePort
from app.adapters.queue.redis import RedisQueueAdapter
from app.adapters.storage.fake import FakeStorageAdapter
from app.adapters.storage.port import StoragePort
from app.adapters.storage.s3 import S3Adapter
from app.adapters.vision.fake import FakeVisionAdapter
from app.adapters.vision.meta import MetaVisionAdapter
from app.adapters.vision.port import VisionPort
from app.main import create_app


def test_fake_language_adapter():
    adapter = FakeLanguageAdapter(default_response="Custom test response")
    assert isinstance(adapter, LanguagePort)
    messages = [Message(role="user", content="Hello comic world")]

    res1 = adapter.complete("meta-llama-3.3-70b-instruct", messages)
    res2 = adapter.complete("meta-llama-3.3-70b-instruct", messages)
    assert res1 == res2
    assert "Custom test response" in res1
    assert "FakeLanguageAdapter" in res1

    tokens = adapter.estimate_tokens(messages)
    assert tokens.input_tokens > 0
    assert tokens.total_tokens == tokens.input_tokens + 50


def test_fake_vision_adapter():
    adapter = FakeVisionAdapter()
    assert isinstance(adapter, VisionPort)
    review = adapter.review("meta-llama-3.2-11b-vision-instruct", b"dummy", "Check line art")
    assert "OK" in review

    palette = adapter.suggest_palette(
        "meta-llama-3.2-11b-vision-instruct", b"dummy", "Cyberpunk alley"
    )
    assert len(palette.palette) > 0
    assert palette.theme != ""
    for item in palette.palette:
        assert item.hex_color.startswith("#")
        assert len(item.hex_color) == 7


def test_fake_image_adapter():
    adapter = FakeImageAdapter()
    assert isinstance(adapter, ImagePort)
    result = adapter.generate(
        prompt="A gritty detective standing in the rain",
        reference_images=[],
        hashed_user_id="user-hash-12345",
        style="noir",
    )
    assert result.image_bytes.startswith(b"\x89PNG")
    assert result.provenance_metadata["c2pa"]["watermark_present"] is True
    assert result.model_id == "muse-image-1.0-fake"
    assert result.request_id.startswith("req-fake-")


def test_fake_storage_adapter():
    adapter = FakeStorageAdapter()
    assert isinstance(adapter, StoragePort)
    key = "test/image.png"
    data = b"image-content-bytes"
    url = adapter.put(key, data, "image/png")
    assert url.endswith(key)
    assert adapter.get(key) == data
    assert adapter.get_content_type(key) == "image/png"

    # Path traversal detection in adapter
    with pytest.raises(ValueError, match="path traversal detected"):
        adapter.put("../outside.png", b"data", "image/png")

    with pytest.raises(ValueError, match="path traversal detected"):
        adapter.get("foo/../../bar.png")

    adapter.delete(key)
    with pytest.raises(KeyError):
        adapter.get(key)


def test_storage_fake_route_serving():
    storage = FakeStorageAdapter()
    app = create_app(
        adapters=AdapterSet(
            language=FakeLanguageAdapter(),
            vision=FakeVisionAdapter(),
            image=FakeImageAdapter(),
            storage=storage,
            queue=FakeQueueAdapter(),
            clock=FakeClockAdapter(),
        )
    )
    client = TestClient(app)

    # 1. Store and retrieve PNG image
    storage.put("drafts/page_1.png", b"\x89PNG-fake-content", "image/png")
    res_png = client.get("/storage/fake/drafts/page_1.png")
    assert res_png.status_code == 200
    assert res_png.headers["content-type"].startswith("image/png")
    assert res_png.content == b"\x89PNG-fake-content"

    # 2. Store and retrieve PDF document
    storage.put("exports/comic.pdf", b"%PDF-1.4-fake-content", "application/pdf")
    res_pdf = client.get("/storage/fake/exports/comic.pdf")
    assert res_pdf.status_code == 200
    assert res_pdf.headers["content-type"].startswith("application/pdf")
    assert res_pdf.content == b"%PDF-1.4-fake-content"

    # 3. 404 on missing object
    res_missing = client.get("/storage/fake/unknown/file.png")
    assert res_missing.status_code == 404
    assert "not found" in res_missing.json()["message"].lower()

    # 4. Path traversal safety returns 400 (or blocked by client normalization)
    res_traversal1 = client.get("/storage/fake/%2e%2e/etc/passwd")
    assert res_traversal1.status_code == 400

    res_traversal2 = client.get("/storage/fake/dir/%2e%2e/%2e%2e/etc/passwd")
    assert res_traversal2.status_code == 400


class InjectedFakeRedisClient:
    """Hermetic in-memory Redis client interface for testing RedisQueueAdapter."""

    def __init__(self):
        self.strings: dict[str, str] = {}
        self.lists: dict[str, list[str]] = {}
        self.sets: dict[str, set[str]] = {}

    def get(self, key: str) -> str | None:
        return self.strings.get(key)

    def set(self, key: str, value: str) -> bool:
        self.strings[key] = value
        return True

    def rpush(self, key: str, *values: str) -> int:
        if key not in self.lists:
            self.lists[key] = []
        for val in values:
            self.lists[key].append(val)
        return len(self.lists[key])

    def lpop(self, key: str) -> str | None:
        items = self.lists.get(key)
        if items and len(items) > 0:
            return items.pop(0)
        return None

    def blpop(self, keys: list[str], timeout: int = 0):
        for k in keys:
            val = self.lpop(k)
            if val is not None:
                return (k, val)
        return None

    def sadd(self, key: str, *values: str) -> int:
        if key not in self.sets:
            self.sets[key] = set()
        self.sets[key].update(values)
        return len(values)


def test_redis_queue_adapter_with_injected_client():
    fake_client = InjectedFakeRedisClient()
    adapter = RedisQueueAdapter(client=fake_client)
    assert isinstance(adapter, QueuePort)

    # 1. Enqueue job
    key = "idemp-redis-task-001"
    payload = {"task": "generate_draft", "page_id": "page-123"}
    job_id1 = adapter.enqueue("image-queue", payload, idempotency_key=key)
    assert job_id1.startswith("job-redis-")

    # 2. Idempotency: duplicate submission returns existing job_id
    job_id2 = adapter.enqueue("image-queue", payload, idempotency_key=key)
    assert job_id1 == job_id2
    assert len(fake_client.lists["queue:image-queue"]) == 1

    # 3. Dequeue pending envelope
    envelope = adapter.dequeue("image-queue", timeout_s=1)
    assert envelope is not None
    assert envelope.job_id == job_id1
    assert envelope.queue == "image-queue"
    assert envelope.payload == payload
    assert envelope.idempotency_key == key
    assert envelope.retry_count == 0

    # 4. Dequeue on empty queue returns None
    assert adapter.dequeue("image-queue", timeout_s=0) is None

    # 5. Acknowledge envelope
    adapter.ack(envelope)
    assert job_id1 in adapter.acknowledged
    assert job_id1 in fake_client.sets["queue:image-queue:ack"]

    # 6. Nack envelope increments retry_count and re-enqueues
    adapter.nack(envelope)
    assert job_id1 in adapter.nacked
    assert envelope.retry_count == 1
    assert len(fake_client.lists["queue:image-queue"]) == 1

    # 7. Dequeue re-queued envelope
    requeued = adapter.dequeue("image-queue")
    assert requeued is not None
    assert requeued.job_id == job_id1
    assert requeued.retry_count == 1


def test_redis_queue_adapter_unconfigured_raises(monkeypatch):
    monkeypatch.delenv("REDIS_URL", raising=False)
    with pytest.raises(RuntimeError, match="not configured"):
        RedisQueueAdapter()


def test_fake_queue_adapter_idempotency():
    adapter = FakeQueueAdapter()
    assert isinstance(adapter, QueuePort)
    key = "idemp-test-key-1"
    job_id1 = adapter.enqueue("image-jobs", {"task": "generate"}, idempotency_key=key)
    job_id2 = adapter.enqueue("image-jobs", {"task": "generate"}, idempotency_key=key)

    assert job_id1 == job_id2

    envelope = adapter.dequeue("image-jobs")
    assert envelope is not None
    assert envelope.job_id == job_id1
    adapter.ack(envelope)
    assert job_id1 in adapter.acknowledged


def test_fake_clock_adapter():
    adapter = FakeClockAdapter()
    assert isinstance(adapter, ClockPort)
    t1 = adapter.now()
    adapter.sleep(10.0)
    t2 = adapter.now()
    assert t2 - t1 == timedelta(seconds=10.0)


def test_system_clock_adapter():
    adapter = SystemClockAdapter()
    assert isinstance(adapter, ClockPort)
    t = adapter.now()
    assert t.tzinfo == timezone.utc


def test_meta_adapters_raise_not_configured():
    with pytest.raises(RuntimeError, match="not configured"):
        MetaLanguageAdapter()

    with pytest.raises(RuntimeError, match="not configured"):
        MetaVisionAdapter()

    with pytest.raises(RuntimeError, match="not configured"):
        MetaImageAdapter()

    with pytest.raises(RuntimeError, match="not configured"):
        S3Adapter()

    with pytest.raises(RuntimeError, match="not configured"):
        RedisQueueAdapter()


def test_adapter_factory_defaults_to_fakes(monkeypatch):
    monkeypatch.delenv("ADAPTER_LANGUAGE", raising=False)
    monkeypatch.delenv("ADAPTER_VISION", raising=False)
    monkeypatch.delenv("ADAPTER_IMAGE", raising=False)
    monkeypatch.delenv("ADAPTER_STORAGE", raising=False)
    monkeypatch.delenv("ADAPTER_QUEUE", raising=False)
    monkeypatch.delenv("ADAPTER_CLOCK", raising=False)

    adapters = build_adapters_from_env()
    assert isinstance(adapters.language, FakeLanguageAdapter)
    assert isinstance(adapters.vision, FakeVisionAdapter)
    assert isinstance(adapters.image, FakeImageAdapter)
    assert isinstance(adapters.storage, FakeStorageAdapter)
    assert isinstance(adapters.queue, FakeQueueAdapter)
    assert isinstance(adapters.clock, FakeClockAdapter)


def test_adapter_factory_invalid_selections():
    with pytest.raises(ValueError, match="Unknown language adapter"):
        get_language_adapter("unknown")

    with pytest.raises(ValueError, match="Unknown vision adapter"):
        get_vision_adapter("unknown")

    with pytest.raises(ValueError, match="Unknown image adapter"):
        get_image_adapter("unknown")

    with pytest.raises(ValueError, match="Unknown storage adapter"):
        get_storage_adapter("unknown")

    with pytest.raises(ValueError, match="Unknown queue adapter"):
        get_queue_adapter("unknown")

    with pytest.raises(ValueError, match="Unknown clock adapter"):
        get_clock_adapter("unknown")
