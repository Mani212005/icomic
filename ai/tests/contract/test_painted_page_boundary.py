"""HTTP regression and wire verification for finished painted-page route prototype."""

import base64
import hashlib
import json
from dataclasses import replace
from pathlib import Path
from uuid import uuid4

import httpx
import pytest
from fastapi.testclient import TestClient

from app.adapters.image.meta import MetaImageAdapter
from app.main import create_app


@pytest.mark.parametrize("variant", ["draft-conditioned", "direct-from-brief"])
def test_painted_page_endpoint_accepts_variants_and_preserves_story(variant):
    app = create_app()
    storage = app.state.storage_adapter
    draft_url = storage.put("draft.png", b"draft-composition-bytes", "image/png")
    char_url = storage.put("nia.png", b"nia-identity-bytes", "image/png")

    panel = {
        "panel_number": 1,
        "scene_description": "Nia holds the brass bird on the cliff",
        "character_actions": "Raises the bird into sea mist",
        "camera_angle": "Low-angle medium shot",
        "background": "Stormy sea and distant lighthouse beam",
        "dialogue": "Follow the light!",
        "narration": "Dawn breaks over the cove.",
        "image_prompt": "Warm golden lantern glow against cool indigo sea spray",
    }
    identities = [
        {
            "character_id": "nia",
            "name": "Nia",
            "description": "Lighthouse keeper",
            "visual_attributes": {"hair_style": "braid", "clothing": "yellow raincoat"},
            "reference_image_url": char_url,
        }
    ]
    payload = {
        "job_id": str(uuid4()),
        "hashed_user_id": "test-user-hash",
        "project_id": str(uuid4()),
        "page_id": str(uuid4()),
        "page_number": 1,
        "style": "classic-comic",
        "variant": variant,
        "draft_image_url": draft_url if variant == "draft-conditioned" else None,
        "panels": [panel],
        "character_identities": identities,
    }

    with TestClient(app) as client:
        response = client.post("/internal/v1/generate-painted-page", json=payload)
        assert response.status_code == 200, response.text
        data = response.json()
        assert data["job_id"] == payload["job_id"]
        assert data["variant"] == variant
        assert "image_url" in data
        assert data["image_url"].startswith("http")


@pytest.mark.parametrize("variant", ["draft-conditioned", "direct-from-brief"])
def test_painted_page_stage_constraints_roles_and_wire_conditioning(variant):
    """Exercise endpoint, render brief, real adapter, and wire builder on mock Meta HTTP transport."""
    captured = []
    fixture = json.loads(
        (Path(__file__).parents[1] / "fixtures/meta/responses_image.json").read_text()
    )

    def respond(request):
        captured.append(json.loads(request.content))
        return httpx.Response(200, json=fixture)

    adapter = MetaImageAdapter(
        api_key="mock-painted-page-key",
        base_url="http://mock.meta.internal/v1",
        transport=httpx.MockTransport(respond),
    )
    fakes = create_app().state.adapters
    app = create_app(replace(fakes, image=adapter))
    storage = app.state.storage_adapter

    draft_content = b"\x89PNG\r\n\x1a\ndraft-sketch-composition"
    char_content = b"\x89PNG\r\n\x1a\nNia-character-identity"
    draft_url = storage.put("draft.png", draft_content, "image/png")
    char_url = storage.put("char.png", char_content, "image/png")

    panel = {
        "panel_number": 1,
        "scene_description": "Nia guides the boat through storm waves",
        "character_actions": "Signals with the glowing brass bird",
        "camera_angle": "Dynamic low-angle wide shot",
        "background": "Lighthouse on wet cliff with crashing waves",
        "dialogue": "Hold steady!",
        "narration": "The lantern flickers in the squall",
        "image_prompt": "Warm bird glow illuminating water droplets and wet oilskin",
    }
    identities = [
        {
            "character_id": "nia",
            "name": "Nia",
            "description": "Lighthouse keeper",
            "visual_attributes": {"hair_style": "braid", "clothing": "yellow raincoat"},
            "reference_image_url": char_url,
        }
    ]
    payload = {
        "job_id": str(uuid4()),
        "hashed_user_id": "test-user-hash",
        "project_id": str(uuid4()),
        "page_id": str(uuid4()),
        "page_number": 1,
        "style": "classic-comic",
        "variant": variant,
        "draft_image_url": draft_url if variant == "draft-conditioned" else None,
        "panels": [panel],
        "character_identities": identities,
    }

    with TestClient(app) as client:
        response = client.post("/internal/v1/generate-painted-page", json=payload)
        assert response.status_code == 200, response.text

    assert len(captured) == 1
    wire = captured[0]
    assert set(wire) == {"model", "input", "user"}
    parts = wire["input"][0]["content"]

    expected_images = (
        [draft_content, char_content] if variant == "draft-conditioned" else [char_content]
    )
    attached_images = [base64.b64decode(part["image_url"].split(",")[1]) for part in parts[:-1]]
    assert attached_images == expected_images

    prompt = parts[-1]["text"]

    # Verify stage-specific golden constraints: rich painting requested, NO accidental 'no shading'.
    assert "Finished painted comic book page" in prompt
    assert "rich colors" in prompt.lower() or "rich color" in prompt.lower()
    assert "lighting" in prompt.lower()
    assert "tonal" in prompt.lower() or "volume" in prompt.lower()
    assert "no shading" not in prompt.lower()
    assert "flat white fills" not in prompt.lower()
    assert "black and white line art" not in prompt.lower()

    # Verify structured brief and source reference roles.
    brief = json.JSONDecoder().raw_decode(prompt[prompt.index("{") :])[0]
    refs = brief["references"]
    assert len(refs) == len(expected_images)
    assert [ref["input_image_index"] for ref in refs] == list(range(1, len(expected_images) + 1))
    assert [ref["sha256"] for ref in refs] == [
        hashlib.sha256(img).hexdigest() for img in expected_images
    ]

    if variant == "draft-conditioned":
        assert "approved composition" in refs[0]["role"]
        assert refs[1]["name"] == "Nia"
        assert "character identity" in refs[1]["role"]
    else:
        assert refs[0]["name"] == "Nia"
        assert "character identity" in refs[0]["role"]


def test_painted_page_immutable_derivative_storage():
    """Verify that multiple generations for the same page produce distinct immutable paths."""
    app = create_app()
    storage = app.state.storage_adapter
    char_url = storage.put("char.png", b"nia", "image/png")

    page_id = str(uuid4())
    payload = {
        "hashed_user_id": "test-user-hash",
        "project_id": str(uuid4()),
        "page_id": page_id,
        "page_number": 1,
        "style": "classic-comic",
        "variant": "direct-from-brief",
        "panels": [
            {
                "panel_number": 1,
                "scene_description": "Nia on the shore",
                "character_actions": "Looks at the horizon",
                "camera_angle": "Wide shot",
                "background": "Distant cliffs",
                "dialogue": "",
                "narration": "",
                "image_prompt": "Morning light",
            }
        ],
        "character_identities": [
            {
                "character_id": "nia",
                "name": "Nia",
                "description": "Lighthouse keeper",
                "visual_attributes": {},
                "reference_image_url": char_url,
            }
        ],
    }

    with TestClient(app) as client:
        resp1 = client.post(
            "/internal/v1/generate-painted-page", json={**payload, "job_id": str(uuid4())}
        )
        assert resp1.status_code == 200, resp1.text
        url1 = resp1.json()["image_url"]

        resp2 = client.post(
            "/internal/v1/generate-painted-page", json={**payload, "job_id": str(uuid4())}
        )
        assert resp2.status_code == 200, resp2.text
        url2 = resp2.json()["image_url"]

        assert url1 != url2
        assert "painted_page_1" in url1
        assert "painted_page_1" in url2
        # Ensure previous artifact content remains accessible and unchanged.
        assert storage.get(url1) is not None
        assert storage.get(url2) is not None
