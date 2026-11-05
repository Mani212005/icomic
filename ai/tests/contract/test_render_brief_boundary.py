"""HTTP regression for story information reaching both image-generation stages."""

from uuid import uuid4

import pytest
from fastapi.testclient import TestClient

from app.main import create_app
from tests.image_fixtures import image_bytes


@pytest.mark.parametrize("stage", ["draft-sketch", "line-art"])
def test_unrelated_stories_produce_different_image_requests(stage):
    app = create_app()
    storage = app.state.storage_adapter
    draft_url = storage.put("draft.png", image_bytes("draft"), "image/png")
    payload = {
        "hashed_user_id": "test-user-hash",
        "project_id": str(uuid4()),
        "page_id": str(uuid4()),
        "page_number": 1,
        "style": "classic-comic",
        "draft_image_url": draft_url,
    }
    stories = [
        {
            "panel_number": 1,
            "scene_description": "Nia rescues a boat in a storm",
            "character_actions": "Raises a brass bird",
            "camera_angle": "Low angle, wide shot",
            "background": "Waves below the lighthouse",
            "dialogue": "Follow the light!",
            "narration": "Before sunrise",
            "image_prompt": "Blue rim light on wet stone",
        },
        {
            "panel_number": 1,
            "scene_description": "A robot buys an apple on Mars",
            "character_actions": "Hands the vendor a coin",
            "camera_angle": "Overhead close-up",
            "background": "A red market under an orange sun",
            "dialogue": "One apple.",
            "narration": "At noon",
            "image_prompt": "Dust on brushed steel",
        },
    ]
    with TestClient(app) as client:
        for panel in stories:
            response = client.post(
                f"/internal/v1/generate-{stage}",
                json={**payload, "job_id": str(uuid4()), "panels": [panel]},
            )
            assert response.status_code == 200, response.text
    calls = app.state.image_adapter.recorded_calls
    assert len(calls) == 2
    assert calls[0]["prompt"] != calls[1]["prompt"]


@pytest.mark.parametrize("stage", ["draft-sketch", "line-art"])
def test_ordered_panels_identity_and_reference_bytes_reach_image_wire(stage):
    """Exercise endpoint, shared brief, real adapter and wire builder on mock HTTP."""
    import base64
    import hashlib
    import json
    from dataclasses import replace
    from pathlib import Path

    import httpx

    from app.adapters.image.meta import MetaImageAdapter

    captured = []
    fixture = json.loads(
        (Path(__file__).parents[1] / "fixtures/meta/responses_image.json").read_text()
    )

    def respond(request):
        captured.append(json.loads(request.content))
        return httpx.Response(200, json=fixture)

    adapter = MetaImageAdapter(
        api_key="mock-render-brief-key",
        base_url="http://mock.meta.internal/v1",
        transport=httpx.MockTransport(respond),
    )
    fakes = create_app().state.adapters
    app = create_app(replace(fakes, image=adapter))
    storage = app.state.storage_adapter
    contents = [
        image_bytes("89504e470d0a1a0a6472616674"),
        image_bytes("89504e470d0a1a0a4e6961"),
        image_bytes("89504e470d0a1a0a62697264"),
    ]
    urls = [storage.put(f"ref{i}.png", content, "image/png") for i, content in enumerate(contents)]
    panels = [
        {
            "panel_number": number,
            "scene_description": f"Beat {number}: Nia and the brass bird",
            "character_actions": f"Action {number}",
            "camera_angle": f"Framing {number}",
            "background": f"Background {number}",
            "dialogue": "Éclaire le bateau!" if number == 9 else "",
            "narration": f"Context {number}",
            "image_prompt": f"Material and light {number}",
        }
        for number in [9, 3, 7, 1, 8, 2, 6, 4]
    ]
    identities = [
        {
            "character_id": "nia",
            "name": "Nia",
            "description": "Lighthouse keeper",
            "visual_attributes": {"hair_style": "braid", "clothing": "yellow raincoat"},
            "reference_image_url": urls[1],
        },
        {
            "character_id": "bird",
            "name": "Brass bird",
            "description": "Small clockwork bird",
            "visual_attributes": {"signature_prop": "warm glowing wings"},
            "reference_image_url": urls[2],
        },
    ]
    payload = {
        "job_id": str(uuid4()),
        "hashed_user_id": "test-user-hash",
        "project_id": str(uuid4()),
        "page_id": str(uuid4()),
        "page_number": 2,
        "style": "classic-comic",
        "draft_image_url": urls[0],
        "panels": panels,
        "character_identities": identities,
        # Old/new callers may send both. Do not attach duplicate reference images.
        "character_reference_image_urls": urls[1:],
    }
    with TestClient(app) as client:
        response = client.post(f"/internal/v1/generate-{stage}", json=payload)
        assert response.status_code == 200, response.text
    assert len(captured) == 1
    wire = captured[0]
    assert set(wire) == {"model", "input", "user"}
    parts = wire["input"][0]["content"]
    expected_images = contents if stage == "line-art" else contents[1:]
    assert [
        base64.b64decode(part["image_url"].split(",")[1]) for part in parts[:-1]
    ] == expected_images
    prompt = parts[-1]["text"]
    # Adapter appends its style string after the brief JSON.
    brief = json.JSONDecoder().raw_decode(prompt[prompt.index("{") :])[0]
    assert brief["panels"] == panels  # All fields and late panels, in allocation order.
    refs = brief["references"]
    assert [ref["input_image_index"] for ref in refs] == list(range(1, len(expected_images) + 1))
    assert [ref["sha256"] for ref in refs] == [
        hashlib.sha256(image).hexdigest() for image in expected_images
    ]
    for ref, identity in zip(refs[-2:], identities, strict=True):
        assert ref["name"] == identity["name"]
        assert ref["visual_attributes"] == identity["visual_attributes"]
        assert ref["source_url"] == identity["reference_image_url"]
    if stage == "line-art":
        assert "approved composition" in refs[0]["role"]
        assert "final inking and line art" in prompt
        assert "Style bible: classic-comic" in prompt
    assert "Only the exact dialogue field" in brief["dialogue_policy"]
    assert "Narration is story context" in brief["dialogue_policy"]
