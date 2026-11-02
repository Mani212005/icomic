"""Unit tests for shared Meta HTTP client, rate limiter, error mapping, and pure functions."""

from __future__ import annotations

import base64
import json
from pathlib import Path
from typing import Any

import httpx
import pytest

from app.adapters.language.port import Message
from app.adapters.meta_http import (
    DEFAULT_IMAGE_COST_USD,
    DEFAULT_META_LANGUAGE_MODEL,
    DEFAULT_META_USER_FIELD,
    MetaConfigurationError,
    MetaContentPolicyError,
    MetaEmptyResponseError,
    MetaHttpClient,
    MetaTransientError,
    TokenBucket,
    build_chat_completion_request,
    build_responses_image_request,
    detect_c2pa_markers,
    detect_image_media_type,
    parse_chat_completion_response,
    parse_palette_json,
    parse_responses_image_response,
)

FIXTURES_DIR = Path(__file__).resolve().parent.parent / "fixtures" / "meta"


def load_fixture(name: str) -> dict[str, Any]:
    with open(FIXTURES_DIR / name, "r", encoding="utf-8") as f:
        return json.load(f)


# ---------------------------------------------------------------------------
# Pure Request Building & Response Parsing Tests
# ---------------------------------------------------------------------------


def test_build_chat_completion_request_structure():
    messages = [
        Message(role="system", content="You are a comic writer."),
        Message(role="user", content="Draft dialogue for panel 1."),
    ]
    payload = build_chat_completion_request(
        model=DEFAULT_META_LANGUAGE_MODEL,
        messages=messages,
        user="hashed_user_12345",
        user_field=DEFAULT_META_USER_FIELD,
        temperature=0.7,
    )

    assert payload["model"] == "muse-spark-1.3"
    assert len(payload["messages"]) == 2
    assert payload["messages"][0] == {"role": "system", "content": "You are a comic writer."}
    assert payload["messages"][1] == {"role": "user", "content": "Draft dialogue for panel 1."}
    assert payload["user"] == "hashed_user_12345"
    assert payload["temperature"] == 0.7


def test_build_chat_completion_request_custom_user_field():
    messages = [Message(role="user", content="Hello")]
    payload = build_chat_completion_request(
        model="test-model",
        messages=messages,
        user="hashed_uid",
        user_field="custom_user_id",
    )
    assert payload["custom_user_id"] == "hashed_uid"
    assert "user" not in payload


def test_parse_chat_completion_response_success():
    fixture = load_fixture("chat_completion.json")
    content, token_usage, request_id, meta = parse_chat_completion_response(fixture)

    assert "Detective: The rain hasn't stopped" in content
    assert token_usage.input_tokens == 16
    assert token_usage.output_tokens == 14
    assert token_usage.total_tokens == 30
    assert request_id == "chatcmpl-mock-lang-001"
    assert meta["model"] == "muse-spark-1.3"


def test_parse_chat_completion_response_content_parts():
    fixture = load_fixture("chat_completion_content_parts.json")
    content, token_usage, _request_id, meta = parse_chat_completion_response(fixture)

    assert "Hello world from part 1." in content
    assert "And greeting from part 2." in content
    assert token_usage.input_tokens == 20
    assert token_usage.output_tokens == 15
    assert token_usage.total_tokens == 35
    assert token_usage.reasoning_tokens == 5
    assert token_usage.finish_reason == "stop"
    assert meta["finish_reason"] == "stop"
    assert meta["reasoning_tokens"] == 5


def test_parse_chat_completion_response_empty_length_raises():
    fixture = load_fixture("chat_completion_empty_length.json")
    with pytest.raises(MetaEmptyResponseError) as exc_info:
        parse_chat_completion_response(fixture)

    err = exc_info.value
    assert "finish_reason='length'" in err.message
    assert err.finish_reason == "length"
    assert err.token_usage is not None
    assert err.token_usage.input_tokens == 14
    assert err.token_usage.output_tokens == 10
    assert err.token_usage.reasoning_tokens == 10
    assert err.request_id == "chatcmpl-mock-lang-empty"


def test_build_responses_image_request_structure_and_encoding():
    ref_image_1 = b"\x89PNG\r\n\x1a\nfake-ref-image-1"
    ref_image_2 = b"\x89PNG\r\n\x1a\nfake-ref-image-2"

    payload = build_responses_image_request(
        model="muse-image-1.0",
        prompt="A noir detective under streetlamp",
        reference_images=[ref_image_1, ref_image_2],
        hashed_user_id="sha256-user-hash",
        style="noir",
        user_field="user",
    )

    assert payload["model"] == "muse-image-1.0"
    assert payload["user"] == "sha256-user-hash"
    assert len(payload["input"]) == 1

    content = payload["input"][0]["content"]
    assert len(content) == 3  # 2 images + 1 prompt text

    # Reference images encoded as base64 data URLs
    assert content[0]["type"] == "input_image"
    b64_1 = base64.b64encode(ref_image_1).decode("ascii")
    assert content[0]["image_url"] == f"data:image/png;base64,{b64_1}"

    assert content[1]["type"] == "input_image"
    b64_2 = base64.b64encode(ref_image_2).decode("ascii")
    assert content[1]["image_url"] == f"data:image/png;base64,{b64_2}"

    # Text prompt with style
    assert content[2]["type"] == "input_text"
    assert "A noir detective under streetlamp. Style: noir" in content[2]["text"]


def test_parse_responses_image_response_and_provenance_preservation():
    fixture = load_fixture("responses_image.json")
    result = parse_responses_image_response(fixture, model_id="muse-image-1.0")

    assert result.request_id == "resp-mock-img-001"
    assert result.model_id == "muse-image-1.0"
    assert result.image_bytes.startswith(b"\x89PNG")

    # Byte-for-byte preservation and embedded C2PA marker detection
    assert b"caBX" in result.image_bytes
    assert detect_c2pa_markers(result.image_bytes) is True

    # Provenance metadata structure
    provenance = result.provenance_metadata
    assert provenance["c2pa"]["watermark_present"] is True
    assert provenance["c2pa"]["manifest_marker_detected"] is True
    assert provenance["c2pa"]["claim_generator"] == "Meta-AI-Provenance/1.0"
    assert provenance["model"] == "muse-image-1.0"
    assert provenance["cost_usd"] == DEFAULT_IMAGE_COST_USD
    assert provenance["usage"]["total_tokens"] == 120
    assert result.media_type == "image/png"


def test_detect_image_media_type():
    assert detect_image_media_type(b"\x89PNG\r\n\x1a\n\x00\x00") == "image/png"
    assert detect_image_media_type(b"\xff\xd8\xff\xe0\x00\x10JFIF") == "image/jpeg"
    assert detect_image_media_type(b"RIFF\x00\x00\x00\x00WEBPVP8 ") == "image/webp"
    assert detect_image_media_type(b"GIF89a\x01\x00\x01\x00") == "image/gif"
    assert detect_image_media_type(b"GIF87a\x01\x00\x01\x00") == "image/gif"
    assert detect_image_media_type(b"unknown binary data stream") == "application/octet-stream"


def test_detect_c2pa_markers_in_jpeg_app11_and_webp():
    # 1. Plain marker substring in arbitrary bytes
    assert detect_c2pa_markers(b"raw-data-with-jumb-marker") is True
    assert detect_c2pa_markers(b"raw-data-with-caBX-marker") is True

    # 2. JPEG with APP11 JUMBF segment (starts with 0xFF 0xEB, length, "JP\x00")
    jpeg_app11 = b"\xff\xd8\xff\xeb\x00\x0cJP\x00\x01testbox\xff\xd9"
    assert detect_c2pa_markers(jpeg_app11) is True

    # 3. WebP RIFF container with JUMB chunk
    webp_jumb = b"RIFF\x20\x00\x00\x00WEBPJUMB\x08\x00\x00\x0012345678"
    assert detect_c2pa_markers(webp_jumb) is True

    # 4. Clean image bytes without C2PA marker return False
    clean_bytes = b"\xff\xd8\xff\xe0\x00\x10JFIF\x00\x01\x01\x00\x00\x01\x00\x01\x00\x00\xff\xd9"
    assert detect_c2pa_markers(clean_bytes) is False


def test_parse_palette_json_valid_and_fallback():
    # Valid JSON inside code block
    raw_response = (
        "Here is the requested palette:\n```json\n"
        '{\n  "theme": "Sunset Highway",\n'
        '  "palette": [\n'
        '    {"region_name": "sky", "hex_color": "#FF5733", "label": "Orange Sky"},\n'
        '    {"region_name": "road", "hex_color": "#1C1C1C", "label": "Asphalt"}\n'
        "  ]\n}\n```"
    )
    suggestion = parse_palette_json(raw_response)
    assert suggestion.theme == "Sunset Highway"
    assert len(suggestion.palette) == 2
    assert suggestion.palette[0].hex_color == "#FF5733"
    assert suggestion.palette[1].region_name == "road"

    # Malformed text falls back gracefully to structured default
    fallback = parse_palette_json("Sorry, I cannot generate colours for this image.")
    assert fallback.theme != ""
    assert len(fallback.palette) >= 2


# ---------------------------------------------------------------------------
# Rate Limiter (TokenBucket) Tests
# ---------------------------------------------------------------------------


class MockClockSleeper:
    def __init__(self, start_time: float = 1000.0):
        self.current_time = start_time
        self.sleep_calls: list[float] = []

    def clock(self) -> float:
        return self.current_time

    def sleeper(self, seconds: float) -> None:
        self.sleep_calls.append(seconds)
        self.current_time += seconds


def test_token_bucket_acquire_and_wait():
    env = MockClockSleeper()
    # 60 tokens per minute = 1 token per second, capacity 2
    bucket = TokenBucket(
        rate_per_minute=60.0,
        capacity=2.0,
        clock=env.clock,
        sleeper=env.sleeper,
    )

    # First two acquisitions succeed immediately
    assert bucket.acquire(1.0) is True
    assert bucket.acquire(1.0) is True
    assert len(env.sleep_calls) == 0

    # Third acquisition requires wait of ~1.0 second
    assert bucket.acquire(1.0) is True
    assert len(env.sleep_calls) >= 1
    assert sum(env.sleep_calls) >= 1.0


def test_token_bucket_timeout():
    env = MockClockSleeper()
    bucket = TokenBucket(
        rate_per_minute=60.0,
        capacity=1.0,
        clock=env.clock,
        sleeper=env.sleeper,
    )
    bucket.acquire(1.0)

    # Attempt acquire with zero timeout when bucket is empty
    acquired = bucket.acquire(1.0, timeout=0.1)
    assert acquired is False


# ---------------------------------------------------------------------------
# Error Mapping & Retry / Backoff Tests
# ---------------------------------------------------------------------------


def test_meta_http_client_content_policy_violation_400():
    policy_err = load_fixture("error_policy.json")

    def mock_handler(request: httpx.Request) -> httpx.Response:
        return httpx.Response(
            status_code=400,
            json=policy_err,
            headers={"x-request-id": "req-policy-01"},
        )

    transport = httpx.MockTransport(mock_handler)
    client = MetaHttpClient(
        api_key="test-key",
        base_url="http://mock.meta.internal/v1",
        transport=transport,
    )

    with pytest.raises(MetaContentPolicyError) as exc_info:
        client.post("/chat/completions", {"prompt": "banned content"})

    err = exc_info.value
    assert err.status_code == 400
    assert err.request_id == "req-policy-01"
    assert "content_policy" in err.message.lower() or "safety" in err.message.lower()


def test_meta_http_client_unrecoverable_config_error_401_403():
    def mock_handler(request: httpx.Request) -> httpx.Response:
        return httpx.Response(status_code=401, json={"error": "Invalid API key"})

    transport = httpx.MockTransport(mock_handler)
    client = MetaHttpClient(
        api_key="bad-key",
        base_url="http://mock.meta.internal/v1",
        transport=transport,
    )

    with pytest.raises(MetaConfigurationError) as exc_info:
        client.post("/chat/completions", {})

    assert exc_info.value.status_code == 401
    assert "authentication or authorization failure" in exc_info.value.message.lower()


def test_meta_http_client_rate_limit_retry_after_and_backoff():
    mock_env = MockClockSleeper()
    rate_limit_err = load_fixture("error_rate_limit.json")
    chat_ok = load_fixture("chat_completion.json")
    attempts = 0

    def mock_handler(request: httpx.Request) -> httpx.Response:
        nonlocal attempts
        attempts += 1
        if attempts < 3:
            return httpx.Response(
                status_code=429,
                json=rate_limit_err,
                headers={"retry-after": "2.5", "x-request-id": f"req-rate-{attempts}"},
            )
        return httpx.Response(
            status_code=200,
            json=chat_ok,
            headers={"x-request-id": "req-ok-final"},
        )

    transport = httpx.MockTransport(mock_handler)
    client = MetaHttpClient(
        api_key="test-key",
        base_url="http://mock.meta.internal/v1",
        transport=transport,
        clock=mock_env.clock,
        sleeper=mock_env.sleeper,
        max_retries=3,
    )

    _data, headers = client.post("/chat/completions", {"prompt": "test"})
    assert attempts == 3
    assert headers.get("x-request-id") == "req-ok-final"
    assert len(mock_env.sleep_calls) == 2
    # Verify Retry-After header delay was respected
    for call in mock_env.sleep_calls:
        assert call >= 2.5


def test_meta_http_client_transient_5xx_retry_and_exhaustion():
    mock_env = MockClockSleeper()
    attempts = 0

    def mock_handler(request: httpx.Request) -> httpx.Response:
        nonlocal attempts
        attempts += 1
        return httpx.Response(status_code=503, text="Upstream Unavailable")

    transport = httpx.MockTransport(mock_handler)
    client = MetaHttpClient(
        api_key="test-key",
        base_url="http://mock.meta.internal/v1",
        transport=transport,
        clock=mock_env.clock,
        sleeper=mock_env.sleeper,
        max_retries=3,
    )

    with pytest.raises(MetaTransientError) as exc_info:
        client.post("/chat/completions", {})

    assert attempts == 4  # Initial + 3 retries
    assert exc_info.value.status_code == 503
    assert len(mock_env.sleep_calls) == 3


def test_meta_http_client_authorization_header_and_privacy():
    captured_auth: list[str] = []

    def mock_handler(request: httpx.Request) -> httpx.Response:
        captured_auth.append(request.headers.get("authorization", ""))
        return httpx.Response(status_code=200, json=load_fixture("chat_completion.json"))

    transport = httpx.MockTransport(mock_handler)
    client = MetaHttpClient(
        api_key="secret-token-xyz-123",
        base_url="http://mock.meta.internal/v1",
        transport=transport,
    )

    client.post("/chat/completions", {"prompt": "sensitive narrative text"})
    assert captured_auth == ["Bearer secret-token-xyz-123"]


# ---------------------------------------------------------------------------
# Strict Network Blocker in Tests
# ---------------------------------------------------------------------------


def test_strict_network_blocker_in_test_transport():
    """Verify that any real socket call in tests is strictly forbidden and raises an error."""
    import socket

    orig_connect = socket.socket.connect

    def blocked_connect(self, *args, **kwargs):
        raise RuntimeError(
            "Hermetic boundary violation: real network socket access is strictly forbidden in tests."
        )

    # With blocked socket, test transport MUST NOT reach out
    socket.socket.connect = blocked_connect
    try:
        # A mocked transport succeeds without touching socket
        def handler(req: httpx.Request) -> httpx.Response:
            return httpx.Response(200, json={"ok": True})

        client = MetaHttpClient(
            api_key="test-key",
            base_url="http://mock.meta.internal/v1",
            transport=httpx.MockTransport(handler),
        )
        data, _ = client.post("/health", {})
        assert data["ok"] is True

        # But a real HTTP client with default transport would trigger the socket blocker
        with pytest.raises(RuntimeError, match="Hermetic boundary violation"):
            real_client = httpx.Client(base_url="http://192.0.2.1:80")
            real_client.get("/test")
    finally:
        socket.socket.connect = orig_connect
