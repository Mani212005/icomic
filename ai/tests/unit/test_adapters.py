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


def test_fake_storage_adapter_storage_base_url_default(monkeypatch):
    monkeypatch.delenv("STORAGE_BASE_URL", raising=False)
    adapter = FakeStorageAdapter()
    assert adapter.base_url == "http://localhost:8000/storage/fake"
    url = adapter.put("exports/123/comic.pdf", b"%PDF-1.4", "application/pdf")
    assert url == "http://localhost:8000/storage/fake/exports/123/comic.pdf"


def test_fake_storage_adapter_storage_base_url_from_env(monkeypatch):
    custom_url = "http://ai-service:9000/storage/fake/"
    monkeypatch.setenv("STORAGE_BASE_URL", custom_url)
    adapter = FakeStorageAdapter()
    assert adapter.base_url == "http://ai-service:9000/storage/fake"
    url = adapter.put("exports/123/comic.pdf", b"%PDF-1.4", "application/pdf")
    assert url == "http://ai-service:9000/storage/fake/exports/123/comic.pdf"
    assert adapter.get(url) == b"%PDF-1.4"


def test_fake_storage_adapter_storage_base_url_explicit_override(monkeypatch):
    monkeypatch.setenv("STORAGE_BASE_URL", "http://env-url:8000/storage/fake")
    adapter = FakeStorageAdapter(base_url="http://explicit-url:8080/storage/fake/")
    assert adapter.base_url == "http://explicit-url:8080/storage/fake"


def test_adapter_factory_storage_base_url(monkeypatch):
    monkeypatch.delenv("STORAGE_BASE_URL", raising=False)
    adapter_default = get_storage_adapter("fake")
    assert isinstance(adapter_default, FakeStorageAdapter)
    assert adapter_default.base_url == "http://localhost:8000/storage/fake"

    monkeypatch.setenv("STORAGE_BASE_URL", "http://custom-ai:8000/storage/fake")
    adapter_custom = get_storage_adapter("fake")
    assert isinstance(adapter_custom, FakeStorageAdapter)
    assert adapter_custom.base_url == "http://custom-ai:8000/storage/fake"


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


def test_redis_queue_adapter_with_injected_client():
    from fakeredis import FakeRedis

    client = FakeRedis()
    adapter = RedisQueueAdapter(client=client)
    assert isinstance(adapter, QueuePort)
    payload = {"task": "generate_draft", "page_id": "page-123", "panels": []}
    first = adapter.enqueue("image-queue", payload, "retry-key")
    assert first.startswith("job-redis-")
    assert adapter.enqueue("image-queue", payload, "retry-key") == first
    assert client.llen("icomic:{image-queue}:ready") == 1
    envelope = adapter.dequeue("image-queue")
    assert envelope is not None
    assert envelope.payload == payload
    assert envelope.retry_count == 0
    assert adapter.dequeue("image-queue") is None
    adapter.nack(envelope)
    envelope = adapter.dequeue("image-queue")
    assert envelope.retry_count == 1
    adapter.finish(envelope, {"status": "done", "response": {"items": []}})
    adapter.ack(envelope)
    restored = RedisQueueAdapter(client=client)
    saved = restored.get_job("image-queue", "retry-key")
    assert saved["status"] == "done"
    assert saved["response"] == {"items": []}
    assert saved["payload"] == payload
    assert restored.enqueue("image-queue", payload, "retry-key") == first
    assert restored.dequeue("image-queue") is None


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
