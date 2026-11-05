"""Golden brief and field-edit invariants for the shared story contract."""

from pathlib import Path

import pytest

from app.adapters.storage.fake import FakeStorageAdapter
from app.gen.models import CharacterRenderIdentity, PanelTextItem
from app.services.render_brief import build_page_render_request
from tests.image_fixtures import image_bytes


def example_request():
    storage = FakeStorageAdapter()
    url = storage.put("characters/nia.png", image_bytes("Nia reference pixels"), "image/png")
    return {
        "page_number": 2,
        "style": "classic-comic",
        "storage": storage,
        "panels": [
            PanelTextItem(
                panel_number=9,
                scene_description="Nia guides a boat through the storm",
                character_actions="Raises the brass bird with both hands",
                camera_angle="Low angle, wide shot",
                background="Waves and wet lighthouse stone",
                dialogue="Suivez la lumière!",
                narration="Before sunrise",
                image_prompt="Warm glow through cool sea mist",
            )
        ],
        "character_identities": [
            CharacterRenderIdentity(
                character_id="nia",
                name="Nia",
                description="Lighthouse keeper",
                visual_attributes={"clothing": "yellow raincoat", "hair_style": "braid"},
                reference_image_url=url,
            )
        ],
    }


def test_golden_shared_render_brief():
    prompt, images = build_page_render_request(**example_request())
    expected = Path(__file__).parents[1] / "fixtures/render_brief_v1.txt"
    assert prompt + "\n" == expected.read_text()
    assert images == [image_bytes("Nia reference pixels")]


@pytest.mark.parametrize("field", list(PanelTextItem.model_fields))
def test_every_panel_field_edit_changes_request(field):
    request = example_request()
    original, _ = build_page_render_request(**request)
    panel = request["panels"][0]
    edited = 42 if field == "panel_number" else "An edited story detail"
    request["panels"] = [panel.model_copy(update={field: edited})]
    changed, _ = build_page_render_request(**request)
    assert original != changed


def test_identity_edits_and_changed_reference_pixels_change_brief():
    request = example_request()
    original, _ = build_page_render_request(**request)
    identity = request["character_identities"][0]
    request["character_identities"] = [
        identity.model_copy(update={"visual_attributes": {"clothing": "blue raincoat"}})
    ]
    changed, _ = build_page_render_request(**request)
    assert original != changed
    request["storage"].put("characters/nia.png", image_bytes("new reference pixels"), "image/png")
    changed_pixels, images = build_page_render_request(**request)
    assert changed != changed_pixels
    assert images == [image_bytes("new reference pixels")]
