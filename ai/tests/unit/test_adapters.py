"""Unit tests for fake adapters (hermetic, no network, deterministic)."""

from datetime import timedelta

from app.adapters.clock.fake import FakeClockAdapter
from app.adapters.image.fake import FakeImageAdapter
from app.adapters.language.fake import FakeLanguageAdapter
from app.adapters.language.port import Message
from app.adapters.queue.fake import FakeQueueAdapter
from app.adapters.storage.fake import FakeStorageAdapter
from app.adapters.vision.fake import FakeVisionAdapter


def test_fake_language_adapter():
    adapter = FakeLanguageAdapter(default_response="Custom test response")
    messages = [Message(role="user", content="Hello comic world")]

    res1 = adapter.complete("meta-llama-3.3-70b-instruct", messages)
    res2 = adapter.complete("meta-llama-3.3-70b-instruct", messages)
    assert res1 == res2
    assert "Custom test response" in res1

    tokens = adapter.estimate_tokens(messages)
    assert tokens.input_tokens > 0
    assert tokens.total_tokens == tokens.input_tokens + 50


def test_fake_vision_adapter():
    adapter = FakeVisionAdapter()
    review = adapter.review("meta-llama-3.2-11b-vision-instruct", b"dummy", "Check line art")
    assert "OK" in review

    palette = adapter.suggest_palette(
        "meta-llama-3.2-11b-vision-instruct", b"dummy", "Cyberpunk alley"
    )
    assert len(palette.palette) > 0
    assert palette.theme != ""
    assert palette.palette[0].hex_color.startswith("#")


def test_fake_image_adapter():
    adapter = FakeImageAdapter()
    result = adapter.generate(
        prompt="A gritty detective standing in the rain",
        reference_images=[],
        hashed_user_id="user-hash-12345",
        style="noir",
    )
    assert result.image_bytes.startswith(b"\x89PNG")
    assert result.provenance_metadata["c2pa"]["watermark_present"] is True
    assert result.model_id == "muse-image-1.0-fake"


def test_fake_storage_adapter():
    adapter = FakeStorageAdapter()
    key = "test/image.png"
    data = b"image-content-bytes"
    url = adapter.put(key, data, "image/png")
    assert url.endswith(key)
    assert adapter.get(key) == data

    adapter.delete(key)
    try:
        adapter.get(key)
        assert False, "expected KeyError"
    except KeyError:
        pass


def test_fake_queue_adapter_idempotency():
    adapter = FakeQueueAdapter()
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
    t1 = adapter.now()
    adapter.sleep(10.0)
    t2 = adapter.now()
    assert t2 - t1 == timedelta(seconds=10.0)
