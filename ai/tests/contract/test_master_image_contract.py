"""Contract tests verifying that image generation records and exposes the actual master-image contract.

Hermetic validation:
- Decoded dimensions, MIME, source/derivative hashes, role, prompt version, response keys, usage.
- Multi-format verification across PNG, JPEG, and WebP.
- No fake quality or seed claims.
- Rejection of unsupported parameters (size, quality, seed, batch count).
- Single-image accounting.
- Derivative tracking when page art cleanup occurs.
"""

from __future__ import annotations

import base64
import hashlib
from io import BytesIO
from uuid import uuid4

import httpx
import pytest
from fastapi.testclient import TestClient
from PIL import Image

from app.adapters.factory import build_adapters_from_env
from app.adapters.image.fake import FakeImageAdapter
from app.adapters.image.meta import MetaImageAdapter
from app.adapters.image.port import ImageResult
from app.adapters.meta_http import (
    DEFAULT_META_IMAGE_MODEL,
    MetaConfigurationError,
    build_responses_image_request,
    detect_c2pa_markers,
    parse_responses_image_response,
)
from app.main import create_app
from app.services.character import CharacterService
from app.services.draft import DraftService
from app.services.images import store_page_image
from app.services.line_art import LineArtService


def make_test_image(format_name: str, size: tuple[int, int] = (120, 80)) -> bytes:
    buffer = BytesIO()
    img = Image.new("RGB", size, (200, 100, 50))
    img.save(buffer, format=format_name)
    return buffer.getvalue()


@pytest.mark.parametrize(
    "format_name,expected_mime",
    [
        ("PNG", "image/png"),
        ("JPEG", "image/jpeg"),
        ("WEBP", "image/webp"),
    ],
)
def test_parse_responses_image_records_contract_across_formats(format_name, expected_mime):
    raw_bytes = make_test_image(format_name, size=(160, 90))
    raw_b64 = base64.b64encode(raw_bytes).decode("ascii")
    raw_hash = hashlib.sha256(raw_bytes).hexdigest()

    response_payload = {
        "id": f"resp-contract-{format_name.lower()}",
        "object": "response",
        "created": 1700000000,
        "model": DEFAULT_META_IMAGE_MODEL,
        "output": [
            {
                "type": "image_generation_call",
                "image_generation_call": {
                    "status": "completed",
                    "result": raw_b64,
                },
            }
        ],
        "usage": {
            "prompt_tokens": 85,
            "completion_tokens": 0,
            "total_tokens": 85,
        },
    }

    result = parse_responses_image_response(response_payload, model_id=DEFAULT_META_IMAGE_MODEL)

    assert isinstance(result, ImageResult)
    assert result.image_bytes == raw_bytes
    assert result.media_type == expected_mime
    assert result.width == 160
    assert result.height == 90

    prov = result.provenance_metadata
    assert prov["dimensions"] == {"width": 160, "height": 90}
    assert prov["media_type"] == expected_mime
    assert prov["source_hash"] == raw_hash
    assert prov["model"] == DEFAULT_META_IMAGE_MODEL
    assert prov["cost_usd"] == 0.01
    assert prov["response_keys"] == ["created", "id", "model", "object", "output", "usage"]
    assert prov["usage"] == {"prompt_tokens": 85, "completion_tokens": 0, "total_tokens": 85}

    # No fake quality or seed claims
    assert "quality" not in prov
    assert "seed" not in prov

    # Honest C2PA reporting: this synthetic image has no C2PA markers
    assert detect_c2pa_markers(raw_bytes) is False
    assert prov["c2pa"]["manifest_marker_detected"] is False
    assert prov["c2pa"]["watermark_present"] is False


@pytest.mark.parametrize(
    "bad_kwarg,bad_val",
    [
        ("size", "1024x1024"),
        ("quality", "hd"),
        ("seed", 42),
        ("n", 2),
        ("aspect_ratio", "16:9"),
    ],
)
def test_image_request_builder_rejects_unsupported_options(bad_kwarg, bad_val):
    kwargs = {bad_kwarg: bad_val}
    with pytest.raises((MetaConfigurationError, ValueError)) as exc:
        build_responses_image_request(
            model=DEFAULT_META_IMAGE_MODEL,
            prompt="A detective in rain",
            **kwargs,
        )
    assert "unsupported" in str(exc.value).lower()


def test_meta_image_adapter_rejects_unsupported_options():
    adapter = MetaImageAdapter(
        api_key="mock-key",
        transport=httpx.MockTransport(lambda req: httpx.Response(200, json={})),
    )
    with pytest.raises((MetaConfigurationError, ValueError)):
        adapter.generate(
            prompt="A detective",
            reference_images=[],
            hashed_user_id="user-1",
            style="noir",
            size="1024x1024",
        )


def test_fake_image_adapter_exposes_master_contract_and_rejects_options():
    fake = FakeImageAdapter()
    result = fake.generate(
        prompt="Test hero",
        reference_images=[],
        hashed_user_id="user-1",
        style="comic",
        role="draft",
        prompt_version="1.0.0",
    )

    assert result.width == 64
    assert result.height == 64
    assert result.media_type == "image/png"
    prov = result.provenance_metadata
    assert prov["dimensions"] == {"width": 64, "height": 64}
    assert prov["media_type"] == "image/png"
    assert prov["source_hash"] == hashlib.sha256(result.image_bytes).hexdigest()
    assert prov["role"] == "draft"
    assert prov["prompt_version"] == "1.0.0"
    assert "quality" not in prov
    assert "seed" not in prov

    # Rejects unsupported options
    with pytest.raises(ValueError):
        fake.generate(
            prompt="Test",
            reference_images=[],
            hashed_user_id="u",
            style="c",
            quality="hd",
        )


def test_services_record_role_and_prompt_version():
    adapters = build_adapters_from_env()

    char_svc = CharacterService(adapters.image, adapters.storage)
    _, _, char_prov = char_svc.generate_reference_image(
        character_id="char-1",
        character_name="Mira",
        prompt="Harbor engineer",
        hashed_user_id="user-1",
        style="comic",
    )
    assert char_prov["role"] == "character_reference"
    assert "prompt_version" in char_prov
    assert char_prov["source_hash"] != ""
    assert char_prov["is_derivative"] is False

    draft_svc = DraftService(adapters.image, adapters.storage)
    _, _, draft_prov = draft_svc.generate_draft_sketch(
        page_id="page-1",
        page_number=1,
        panels=[],
        style="comic",
        hashed_user_id="user-1",
    )
    assert draft_prov["role"] == "draft"
    assert "prompt_version" in draft_prov
    assert draft_prov["source_hash"] != ""

    line_svc = LineArtService(adapters.image, adapters.storage)
    draft_url = adapters.storage.put("draft.png", make_test_image("PNG"), "image/png")
    _, _, line_prov = line_svc.generate_line_art(
        page_id="page-1",
        page_number=1,
        draft_image_url=draft_url,
        panels=[],
        style="comic",
        hashed_user_id="user-1",
    )
    assert line_prov["role"] == "line_art"
    assert "prompt_version" in line_prov
    assert line_prov["source_hash"] != ""


def test_page_art_cleanup_records_source_and_derivative_hash():
    from PIL import ImageDraw, ImageFont

    from tests.unit.test_page_art import synthetic_page

    image = synthetic_page()
    ImageDraw.Draw(image).text(
        (100, 40), "NOTES AND LABELS", font=ImageFont.load_default(size=16), fill="black"
    )
    buf = BytesIO()
    image.save(buf, format="PNG")
    raw_bytes = buf.getvalue()
    raw_hash = hashlib.sha256(raw_bytes).hexdigest()

    adapters = build_adapters_from_env()
    fake_result = ImageResult(
        image_bytes=raw_bytes,
        provenance_metadata={
            "source_hash": raw_hash,
            "dimensions": {"width": image.width, "height": image.height},
            "media_type": "image/png",
            "role": "line_art",
            "prompt_version": "1.0.0",
        },
        model_id="test-model",
        request_id="req-clean",
        media_type="image/png",
        width=image.width,
        height=image.height,
    )

    url = store_page_image(adapters.storage, "pages/test/line_art_1", fake_result)
    assert url.endswith(".png")

    prov = fake_result.provenance_metadata
    assert prov["is_derivative"] is True
    assert prov["source_hash"] == raw_hash
    assert prov["derivative_hash"] != raw_hash
    assert prov["raw_image_url"].endswith("_raw.png")


def test_fastapi_endpoints_expose_master_contract():
    adapters = build_adapters_from_env()
    app = create_app(adapters)
    client = TestClient(app)

    # Character reference endpoint
    resp = client.post(
        "/internal/v1/generate-character-reference",
        json={
            "job_id": str(uuid4()),
            "project_id": str(uuid4()),
            "hashed_user_id": "test-user",
            "character_id": str(uuid4()),
            "character_name": "Mira",
            "prompt": "Harbor engineer",
            "style": "comic",
        },
    )
    assert resp.status_code == 200, resp.text
    prov = resp.json()["provenance_metadata"]
    assert prov["role"] == "character_reference"
    assert "prompt_version" in prov
    assert "dimensions" in prov
    assert "source_hash" in prov
    assert "media_type" in prov
    assert "response_keys" in prov
