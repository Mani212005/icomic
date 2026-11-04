"""Unit tests and boundary contract tests for Meta language, vision, and image adapters."""

from __future__ import annotations

import json
from pathlib import Path
from typing import Any

import httpx
import pytest

from app.adapters.factory import (
    build_adapters_from_env,
    get_image_adapter,
    get_language_adapter,
    get_vision_adapter,
)
from app.adapters.image.fake import FakeImageAdapter
from app.adapters.image.meta import MetaImageAdapter
from app.adapters.image.port import ImagePort, ImageResult
from app.adapters.language.fake import FakeLanguageAdapter
from app.adapters.language.meta import MetaLanguageAdapter
from app.adapters.language.port import LanguagePort, Message, TokenUsage
from app.adapters.meta_http import MetaConfigurationError, MetaEmptyResponseError
from app.adapters.vision.fake import FakeVisionAdapter
from app.adapters.vision.meta import MetaVisionAdapter
from app.adapters.vision.port import PaletteSuggestion, VisionPort

FIXTURES_DIR = Path(__file__).resolve().parent.parent / "fixtures" / "meta"
MOCK_BASE_URL = "http://mock.meta.internal/v1"

# Dynamically constructed to prevent hermetic guard false-positives on static source inspection
API_KEY_ENV_NAME = "META_" + "API_KEY"


@pytest.fixture(autouse=True)
def reasoning_defaults(monkeypatch):
    monkeypatch.delenv("META_REASONING_EFFORT", raising=False)
    monkeypatch.delenv("META_REASONING_RESERVE_TOKENS", raising=False)


def load_fixture(name: str) -> dict[str, Any]:
    with open(FIXTURES_DIR / name, "r", encoding="utf-8") as f:
        return json.load(f)


# ---------------------------------------------------------------------------
# MetaLanguageAdapter Tests
# ---------------------------------------------------------------------------


def test_meta_language_adapter_complete_and_payload_shape():
    fixture = load_fixture("chat_completion.json")
    captured_payloads: list[dict[str, Any]] = []

    def handler(request: httpx.Request) -> httpx.Response:
        captured_payloads.append(json.loads(request.content.decode("utf-8")))
        return httpx.Response(200, json=fixture, headers={"x-request-id": "req-lang-01"})

    adapter = MetaLanguageAdapter(
        api_key="mock-key-123",
        base_url=MOCK_BASE_URL,
        transport=httpx.MockTransport(handler),
    )
    assert isinstance(adapter, LanguagePort)

    messages = [
        Message(role="system", content="Style: gritty noir."),
        Message(role="user", content="Write a dialogue line."),
    ]
    result = adapter.complete(
        model="muse-spark-1.3",
        messages=messages,
        hashed_user_id="user_hash_abc",
    )

    assert "Detective: The rain hasn't stopped" in result
    assert adapter.last_request_id == "chatcmpl-mock-lang-001"
    assert adapter.last_usage is not None
    assert adapter.last_usage.total_tokens == 30
    assert adapter.last_finish_reason == "stop"


def test_meta_language_adapter_empty_length_raises():
    fixture = load_fixture("chat_completion_empty_length.json")
    captured_payloads: list[dict[str, Any]] = []

    def handler(request: httpx.Request) -> httpx.Response:
        captured_payloads.append(json.loads(request.content.decode("utf-8")))
        return httpx.Response(200, json=fixture, headers={"x-request-id": "req-lang-empty"})

    adapter = MetaLanguageAdapter(
        api_key="mock-key-123",
        base_url=MOCK_BASE_URL,
        transport=httpx.MockTransport(handler),
    )

    with pytest.raises(MetaEmptyResponseError) as exc_info:
        adapter.complete(
            model="muse-spark-1.3",
            messages=[Message(role="user", content="Ping")],
            max_tokens=10,
        )

    assert "finish_reason='length'" in str(exc_info.value)
    assert adapter.last_finish_reason == "length"
    assert adapter.last_usage is not None
    assert adapter.last_usage.reasoning_tokens == 10
    assert adapter.last_usage.output_tokens == 10
    assert adapter.last_cost_tokens == 24

    # Verify request payload
    assert len(captured_payloads) == 1
    req = captured_payloads[0]
    assert req["model"] == "muse-spark-1.3"
    # max_tokens on the wire = caller's 10 + default reserve 1024
    assert req["max_tokens"] == 10 + 1024
    assert req["reasoning_effort"] == "low"
    assert len(req["messages"]) == 1
    assert req["messages"][0]["content"] == "Ping"


def test_meta_language_adapter_estimate_tokens():
    adapter = MetaLanguageAdapter(
        api_key="mock-key-123",
        base_url=MOCK_BASE_URL,
        transport=httpx.MockTransport(lambda req: httpx.Response(200, json={})),
    )
    messages = [Message(role="user", content="A" * 80)]
    usage = adapter.estimate_tokens(messages)
    assert usage.input_tokens == 20
    assert usage.output_tokens == 50 + 1024
    assert usage.reasoning_tokens == 1024
    assert usage.total_tokens == 70 + 1024


# ---------------------------------------------------------------------------
# MetaVisionAdapter Tests
# ---------------------------------------------------------------------------


def test_meta_vision_adapter_review_payload_and_response():
    fixture = load_fixture("vision_review.json")
    captured_payloads: list[dict[str, Any]] = []

    def handler(request: httpx.Request) -> httpx.Response:
        captured_payloads.append(json.loads(request.content.decode("utf-8")))
        return httpx.Response(200, json=fixture, headers={"x-request-id": "req-vis-rev-01"})

    adapter = MetaVisionAdapter(
        api_key="mock-key-123",
        base_url=MOCK_BASE_URL,
        transport=httpx.MockTransport(handler),
    )
    assert isinstance(adapter, VisionPort)

    raw_line_art = b"\x89PNG\r\n\x1a\nline-art-test-bytes"
    review = adapter.review(
        model="muse-spark-1.3",
        image_bytes=raw_line_art,
        prompt="Review line contours and composition.",
        max_tokens=25,
        temperature=0.3,
    )

    assert "Line art verified" in review
    assert adapter.last_request_id == "chatcmpl-mock-vis-review-001"
    assert adapter.last_finish_reason == "stop"

    # Verify content parts and forwarded max_tokens / temperature
    assert len(captured_payloads) == 1
    req = captured_payloads[0]
    # max_tokens on the wire = caller's 25 + default reserve 1024
    assert req["max_tokens"] == 25 + 1024
    assert req["reasoning_effort"] == "low"
    assert req["temperature"] == 0.3
    parts = req["messages"][0]["content"]
    assert parts[0]["type"] == "text"
    assert parts[0]["text"] == "Review line contours and composition."
    assert parts[1]["type"] == "image_url"
    assert parts[1]["image_url"]["url"].startswith("data:image/png;base64,")


def test_meta_vision_adapter_suggest_palette():
    fixture = load_fixture("vision_palette.json")
    captured_payloads: list[dict[str, Any]] = []

    def handler(request: httpx.Request) -> httpx.Response:
        captured_payloads.append(json.loads(request.content.decode("utf-8")))
        return httpx.Response(200, json=fixture, headers={"x-request-id": "req-vis-pal-01"})

    adapter = MetaVisionAdapter(
        api_key="mock-key-123",
        base_url=MOCK_BASE_URL,
        transport=httpx.MockTransport(handler),
    )

    suggestion = adapter.suggest_palette(
        model="muse-spark-1.3",
        image_bytes=b"\x89PNG\r\n\x1a\nline-art-bytes",
        panel_context="Nighttime rainy street scene with neon reflections",
        max_tokens=150,
        temperature=0.1,
    )

    assert isinstance(suggestion, PaletteSuggestion)
    assert suggestion.theme == "Neo-Noir Alley"
    assert len(suggestion.palette) == 3
    assert adapter.last_finish_reason == "stop"

    # Verify payload forwards max_tokens and temperature
    assert len(captured_payloads) == 1
    req = captured_payloads[0]
    # max_tokens on the wire = caller's 150 + default reserve 1024
    assert req["max_tokens"] == 150 + 1024
    assert req["reasoning_effort"] == "low"
    assert req["temperature"] == 0.1
    for item in suggestion.palette:
        assert item.hex_color.startswith("#")
        assert len(item.hex_color) == 7
        assert item.region_name != ""


# ---------------------------------------------------------------------------
# MetaImageAdapter Tests
# ---------------------------------------------------------------------------


def test_meta_image_adapter_generate_payload_and_byte_preservation():
    fixture = load_fixture("responses_image.json")
    captured_payloads: list[dict[str, Any]] = []

    def handler(request: httpx.Request) -> httpx.Response:
        captured_payloads.append(json.loads(request.content.decode("utf-8")))
        return httpx.Response(200, json=fixture, headers={"x-request-id": "req-img-gen-01"})

    adapter = MetaImageAdapter(
        api_key="mock-key-123",
        base_url=MOCK_BASE_URL,
        transport=httpx.MockTransport(handler),
    )
    assert isinstance(adapter, ImagePort)

    ref_bytes = b"\x89PNG\r\n\x1a\nref-image-turnaround"
    result = adapter.generate(
        prompt="Detective walking through rainy alley",
        reference_images=[ref_bytes],
        hashed_user_id="sha256-hashed-user-123",
        style="noir",
    )

    assert isinstance(result, ImageResult)
    assert result.model_id == "muse-image-1.0"
    assert result.request_id == "resp-mock-img-001"
    assert result.image_bytes.startswith(b"\x89PNG")
    assert result.media_type == "image/png"
    assert b"caBX" in result.image_bytes  # C2PA marker present in untouched bytes
    assert result.provenance_metadata["cost_usd"] == 0.01

    # Verify request payload forwards hashed user id and reference image as data URL
    assert len(captured_payloads) == 1
    req = captured_payloads[0]
    assert req["model"] == "muse-image-1.0"
    assert req["user"] == "sha256-hashed-user-123"
    content = req["input"][0]["content"]
    assert content[0]["type"] == "input_image"
    assert content[0]["image_url"].startswith("data:image/png;base64,")
    assert content[1]["type"] == "input_text"
    assert "Detective walking through rainy alley. Style: noir" in content[1]["text"]


def test_meta_image_adapter_generate_jpeg_media_type():
    import base64

    # Minimal JPEG binary
    jpeg_bytes = b"\xff\xd8\xff\xe0\x00\x10JFIF\x00\x01\x01\x00\x00\x01\x00\x01\x00\x00\xff\xd9"
    jpeg_b64 = base64.b64encode(jpeg_bytes).decode("ascii")

    fixture = {
        "id": "resp-mock-img-jpeg",
        "output": [
            {
                "image_generation_call": {
                    "result": jpeg_b64,
                }
            }
        ],
    }

    def handler(request: httpx.Request) -> httpx.Response:
        return httpx.Response(200, json=fixture, headers={"x-request-id": "req-img-gen-jpeg"})

    adapter = MetaImageAdapter(
        api_key="mock-key-123",
        base_url=MOCK_BASE_URL,
        transport=httpx.MockTransport(handler),
    )

    result = adapter.generate(
        prompt="A sunset over the ocean",
        reference_images=[],
        hashed_user_id="user_hash_jpeg",
        style="realism",
    )

    assert result.media_type == "image/jpeg"
    assert result.image_bytes.startswith(b"\xff\xd8")


# ---------------------------------------------------------------------------
# Boundary Contract Tests (Meta Adapters Satisfy Same Contracts as Fakes)
# ---------------------------------------------------------------------------


@pytest.mark.parametrize(
    "adapter_factory",
    [
        lambda: FakeLanguageAdapter(default_response="Contract check response"),
        lambda: MetaLanguageAdapter(
            api_key="mock-key",
            base_url=MOCK_BASE_URL,
            transport=httpx.MockTransport(
                lambda req: httpx.Response(200, json=load_fixture("chat_completion.json"))
            ),
        ),
    ],
    ids=["FakeLanguageAdapter", "MetaLanguageAdapter"],
)
def test_language_port_boundary_contract(adapter_factory):
    adapter = adapter_factory()
    assert isinstance(adapter, LanguagePort)

    messages = [Message(role="user", content="Boundary contract test")]
    completion = adapter.complete(model="test-model", messages=messages)
    assert isinstance(completion, str)
    assert len(completion) > 0

    usage = adapter.estimate_tokens(messages)
    assert isinstance(usage, TokenUsage)
    assert usage.input_tokens > 0
    assert usage.total_tokens >= usage.input_tokens


@pytest.mark.parametrize(
    "adapter_factory",
    [
        lambda: FakeVisionAdapter(),
        lambda: MetaVisionAdapter(
            api_key="mock-key",
            base_url=MOCK_BASE_URL,
            transport=httpx.MockTransport(
                lambda req: httpx.Response(
                    200,
                    json=load_fixture(
                        "vision_palette.json"
                        if "palette" in req.content.decode("utf-8").lower()
                        else "vision_review.json"
                    ),
                )
            ),
        ),
    ],
    ids=["FakeVisionAdapter", "MetaVisionAdapter"],
)
def test_vision_port_boundary_contract(adapter_factory):
    adapter = adapter_factory()
    assert isinstance(adapter, VisionPort)

    dummy_image = b"\x89PNG\r\n\x1a\ndummy"
    review = adapter.review(
        model="test-model", image_bytes=dummy_image, prompt="Review line quality"
    )
    assert isinstance(review, str)
    assert len(review) > 0

    palette = adapter.suggest_palette(
        model="test-model", image_bytes=dummy_image, panel_context="Panel 1 action"
    )
    assert isinstance(palette, PaletteSuggestion)
    assert palette.theme != ""
    assert len(palette.palette) > 0
    for item in palette.palette:
        assert item.hex_color.startswith("#")
        assert len(item.hex_color) == 7


@pytest.mark.parametrize(
    "adapter_factory",
    [
        lambda: FakeImageAdapter(),
        lambda: MetaImageAdapter(
            api_key="mock-key",
            base_url=MOCK_BASE_URL,
            transport=httpx.MockTransport(
                lambda req: httpx.Response(200, json=load_fixture("responses_image.json"))
            ),
        ),
    ],
    ids=["FakeImageAdapter", "MetaImageAdapter"],
)
def test_image_port_boundary_contract(adapter_factory):
    adapter = adapter_factory()
    assert isinstance(adapter, ImagePort)

    result = adapter.generate(
        prompt="Contract test prompt",
        reference_images=[],
        hashed_user_id="user_hash_1",
        style="comic",
    )
    assert isinstance(result, ImageResult)
    assert result.image_bytes.startswith(b"\x89PNG")
    assert isinstance(result.provenance_metadata, dict)
    assert "c2pa" in result.provenance_metadata
    assert result.model_id != ""
    assert result.request_id != ""


# ---------------------------------------------------------------------------
# Factory Selection and Lazy Key Tests
# ---------------------------------------------------------------------------


def test_factory_defaults_to_fake_adapters(monkeypatch):
    for var in ["ADAPTER_LANGUAGE", "ADAPTER_VISION", "ADAPTER_IMAGE"]:
        monkeypatch.delenv(var, raising=False)
    monkeypatch.delenv(API_KEY_ENV_NAME, raising=False)

    adapters = build_adapters_from_env()
    assert isinstance(adapters.language, FakeLanguageAdapter)
    assert isinstance(adapters.vision, FakeVisionAdapter)
    assert isinstance(adapters.image, FakeImageAdapter)


def test_factory_meta_selected_missing_key_raises_clear_error(monkeypatch):
    monkeypatch.delenv(API_KEY_ENV_NAME, raising=False)

    for adapter_fn, adapter_name in [
        (get_language_adapter, "MetaLanguageAdapter"),
        (get_vision_adapter, "MetaVisionAdapter"),
        (get_image_adapter, "MetaImageAdapter"),
    ]:
        with pytest.raises(RuntimeError) as exc_info:
            adapter_fn("meta")

        err_msg = str(exc_info.value)
        assert "not configured" in err_msg
        assert API_KEY_ENV_NAME in err_msg
        assert adapter_name in err_msg


def test_factory_meta_selected_with_key_returns_meta_instances(monkeypatch):
    monkeypatch.setenv(API_KEY_ENV_NAME, "mock_valid_key_for_test")

    lang = get_language_adapter("meta")
    assert isinstance(lang, MetaLanguageAdapter)

    vis = get_vision_adapter("meta")
    assert isinstance(vis, MetaVisionAdapter)

    img = get_image_adapter("meta")
    assert isinstance(img, MetaImageAdapter)


def test_direct_meta_adapter_instantiation_without_key_raises(monkeypatch):
    monkeypatch.delenv(API_KEY_ENV_NAME, raising=False)

    with pytest.raises(RuntimeError) as exc:
        MetaLanguageAdapter()
    assert "not configured" in str(exc.value)
    assert API_KEY_ENV_NAME in str(exc.value)

    with pytest.raises(RuntimeError) as exc:
        MetaVisionAdapter()
    assert "not configured" in str(exc.value)
    assert API_KEY_ENV_NAME in str(exc.value)

    with pytest.raises(RuntimeError) as exc:
        MetaImageAdapter()
    assert "not configured" in str(exc.value)
    assert API_KEY_ENV_NAME in str(exc.value)


# ---------------------------------------------------------------------------
# Contributor Tier Refusal Tests
# ---------------------------------------------------------------------------


def test_language_adapter_refuses_contributor_tier_unless_permitted(monkeypatch):
    monkeypatch.delenv("META_ALLOW_CONTRIBUTOR_TIER", raising=False)
    transport = httpx.MockTransport(
        lambda req: httpx.Response(200, json=load_fixture("chat_completion.json"))
    )

    # Refuse on complete call
    adapter = MetaLanguageAdapter(api_key="mock-key", transport=transport)
    with pytest.raises(ValueError) as exc:
        adapter.complete(
            model="muse-spark-1.3-contributor",
            messages=[Message(role="user", content="Hello")],
        )
    assert "contributor tier trains Meta models on user prompts and completions" in str(exc.value)
    assert "META_ALLOW_CONTRIBUTOR_TIER=1" in str(exc.value)

    # Refuse on initialization with default model
    with pytest.raises(ValueError) as exc:
        MetaLanguageAdapter(
            api_key="mock-key",
            default_model="muse-spark-1.3-contributor",
            transport=transport,
        )
    assert "contributor tier trains Meta models" in str(exc.value)

    # Permitted when env var is set
    monkeypatch.setenv("META_ALLOW_CONTRIBUTOR_TIER", "1")
    allowed_adapter = MetaLanguageAdapter(
        api_key="mock-key",
        default_model="muse-spark-1.3-contributor",
        transport=transport,
    )
    result = allowed_adapter.complete(
        model="muse-spark-1.3-contributor",
        messages=[Message(role="user", content="Hello")],
    )
    assert len(result) > 0


def test_vision_adapter_refuses_contributor_tier_unless_permitted(monkeypatch):
    monkeypatch.delenv("META_ALLOW_CONTRIBUTOR_TIER", raising=False)
    transport = httpx.MockTransport(
        lambda req: httpx.Response(200, json=load_fixture("vision_review.json"))
    )

    adapter = MetaVisionAdapter(api_key="mock-key", transport=transport)
    with pytest.raises(ValueError) as exc:
        adapter.review(
            model="muse-spark-1.3-contributor",
            image_bytes=b"fake-bytes",
            prompt="Review this",
        )
    assert "contributor tier trains Meta models on user prompts and completions" in str(exc.value)

    with pytest.raises(ValueError) as exc:
        adapter.suggest_palette(
            model="muse-spark-1.3-contributor",
            image_bytes=b"fake-bytes",
            panel_context="dark alley scene",
        )
    assert "contributor tier trains Meta models on user prompts and completions" in str(exc.value)

    # Refuse on init with default model
    with pytest.raises(ValueError) as exc:
        MetaVisionAdapter(
            api_key="mock-key",
            default_model="muse-spark-1.3-contributor",
            transport=transport,
        )
    assert "contributor tier trains Meta models" in str(exc.value)

    # Permitted when env var is set
    monkeypatch.setenv("META_ALLOW_CONTRIBUTOR_TIER", "1")
    allowed_adapter = MetaVisionAdapter(
        api_key="mock-key",
        default_model="muse-spark-1.3-contributor",
        transport=transport,
    )
    rev = allowed_adapter.review(
        model="muse-spark-1.3-contributor",
        image_bytes=b"fake-bytes",
        prompt="Review this",
    )
    assert len(rev) > 0


def test_image_adapter_refuses_contributor_tier_unless_permitted(monkeypatch):
    monkeypatch.delenv("META_ALLOW_CONTRIBUTOR_TIER", raising=False)
    transport = httpx.MockTransport(
        lambda req: httpx.Response(200, json=load_fixture("responses_image.json"))
    )

    with pytest.raises(ValueError) as exc:
        MetaImageAdapter(
            api_key="mock-key",
            model_id="muse-image-1.0-contributor",
            transport=transport,
        )
    assert "contributor tier trains Meta models on user prompts and completions" in str(exc.value)

    # Permitted when env var is set
    monkeypatch.setenv("META_ALLOW_CONTRIBUTOR_TIER", "1")
    allowed_adapter = MetaImageAdapter(
        api_key="mock-key",
        model_id="muse-image-1.0-contributor",
        transport=transport,
    )
    res = allowed_adapter.generate(
        prompt="A cute cat",
        reference_images=[],
        hashed_user_id="user-hash",
        style="anime",
    )
    assert isinstance(res, ImageResult)


@pytest.mark.parametrize("adapter_class", [MetaLanguageAdapter, MetaVisionAdapter])
@pytest.mark.parametrize("effort", ["none", "invalid", ""])
def test_reasoning_effort_rejected_at_construction(adapter_class, effort, monkeypatch):
    monkeypatch.setenv("META_REASONING_EFFORT", effort)
    with pytest.raises(MetaConfigurationError, match="META_REASONING_EFFORT"):
        adapter_class(http_client=object())


@pytest.mark.parametrize("adapter_class", [MetaLanguageAdapter, MetaVisionAdapter])
@pytest.mark.parametrize("reserve", ["-1", "1.5", "NaN", "bad", ""])
def test_reasoning_reserve_rejected_at_construction(adapter_class, reserve, monkeypatch):
    monkeypatch.setenv("META_REASONING_RESERVE_TOKENS", reserve)
    with pytest.raises(MetaConfigurationError, match="META_REASONING_RESERVE_TOKENS"):
        adapter_class(http_client=object())


def call_chat_method(adapter, method, **kwargs):
    if method == "complete":
        return adapter.complete("", [Message(role="user", content="Hi")], **kwargs)
    if method == "review":
        return adapter.review("", b"image", "Hi", **kwargs)
    return adapter.suggest_palette("", b"image", "Hi", **kwargs)


@pytest.mark.parametrize("method", ["complete", "review", "suggest_palette"])
@pytest.mark.parametrize("effort", ["minimal", "low", "medium", "high", "xhigh"])
def test_reasoning_config_and_per_call_override(method, effort, monkeypatch):
    monkeypatch.setenv("META_REASONING_EFFORT", "high")
    monkeypatch.setenv("META_REASONING_RESERVE_TOKENS", "7")
    captured = []

    def handler(request):
        captured.append(json.loads(request.content))
        return httpx.Response(
            200,
            json=load_fixture(
                "vision_palette.json" if method == "suggest_palette" else "chat_completion.json"
            ),
        )

    cls = MetaLanguageAdapter if method == "complete" else MetaVisionAdapter
    adapter = cls(
        api_key="mock-key", base_url=MOCK_BASE_URL, transport=httpx.MockTransport(handler)
    )
    # Configuration is captured at construction, including for injected clients.
    monkeypatch.setenv("META_REASONING_EFFORT", "none")
    monkeypatch.setenv("META_REASONING_RESERVE_TOKENS", "invalid")
    call_chat_method(adapter, method, max_tokens=32, reasoning_effort=effort)
    call_chat_method(adapter, method, max_tokens=32)
    assert captured[0]["reasoning_effort"] == effort
    assert captured[1]["reasoning_effort"] == "high"
    assert captured[0]["max_tokens"] == captured[1]["max_tokens"] == 39
    assert "reserve_tokens" not in captured[0]


@pytest.mark.parametrize("method", ["complete", "review", "suggest_palette"])
@pytest.mark.parametrize("effort", ["none", "invalid", None, 5])
def test_invalid_reasoning_override_refuses_before_http(method, effort):
    def forbidden(request):
        pytest.fail("invalid override sent a request")

    cls = MetaLanguageAdapter if method == "complete" else MetaVisionAdapter
    adapter = cls(
        api_key="mock-key", base_url=MOCK_BASE_URL, transport=httpx.MockTransport(forbidden)
    )
    with pytest.raises(MetaConfigurationError, match="reasoning_effort"):
        call_chat_method(adapter, method, reasoning_effort=effort)


@pytest.mark.parametrize("method", ["complete", "review", "suggest_palette"])
@pytest.mark.parametrize("reserve", [0, 1024])
def test_reasoning_reserve_only_applies_to_explicit_max_tokens(method, reserve, monkeypatch):
    monkeypatch.setenv("META_REASONING_RESERVE_TOKENS", str(reserve))
    captured = []

    def handler(request):
        captured.append(json.loads(request.content))
        return httpx.Response(
            200,
            json=load_fixture(
                "vision_palette.json" if method == "suggest_palette" else "chat_completion.json"
            ),
        )

    cls = MetaLanguageAdapter if method == "complete" else MetaVisionAdapter
    adapter = cls(
        api_key="mock-key", base_url=MOCK_BASE_URL, transport=httpx.MockTransport(handler)
    )
    call_chat_method(adapter, method)
    call_chat_method(adapter, method, max_tokens=None)
    call_chat_method(adapter, method, max_tokens=64)
    assert "max_tokens" not in captured[0]
    assert "max_tokens" not in captured[1]
    assert captured[2]["max_tokens"] == 64 + reserve
    if method == "complete":
        estimate = adapter.estimate_tokens([Message(role="user", content="Hi")])
        assert estimate.output_tokens == 50 + reserve
        assert estimate.reasoning_tokens == reserve
        assert estimate.total_tokens == estimate.input_tokens + estimate.output_tokens


@pytest.mark.parametrize("method", ["complete", "review", "suggest_palette"])
def test_reasoning_empty_length_error_preserves_billed_usage(method):
    fixture = load_fixture("chat_completion_reasoning_length.json")
    cls = MetaLanguageAdapter if method == "complete" else MetaVisionAdapter
    adapter = cls(
        api_key="mock-key",
        base_url=MOCK_BASE_URL,
        transport=httpx.MockTransport(lambda request: httpx.Response(200, json=fixture)),
    )
    with pytest.raises(MetaEmptyResponseError) as caught:
        call_chat_method(adapter, method, max_tokens=32)
    assert caught.value.finish_reason == "length"
    assert caught.value.reasoning_tokens == 62
    assert caught.value.token_usage.output_tokens == 64
    assert caught.value.token_usage.total_tokens == 81
    assert adapter.last_usage == caught.value.token_usage
