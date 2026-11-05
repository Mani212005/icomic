"""Unit tests for painted page service, prompt constraints, and variants."""

from app.adapters.image.fake import FakeImageAdapter
from app.adapters.storage.fake import FakeStorageAdapter
from app.gen.models import CharacterRenderIdentity, PanelTextItem
from app.services.painted_page import (
    VARIANT_DIRECT_FROM_BRIEF,
    VARIANT_DRAFT_CONDITIONED,
    PaintedPageService,
)
from tests.image_fixtures import image_bytes


def make_service():
    storage = FakeStorageAdapter()
    image = FakeImageAdapter()
    service = PaintedPageService(image, storage)
    return service, storage, image


def sample_panels():
    return [
        PanelTextItem(
            panel_number=1,
            scene_description="Nia stands on the storm cliff",
            character_actions="Holds up the glowing brass bird",
            camera_angle="Low-angle heroic framing",
            background="Crashing waves against basalt rock",
            dialogue="Nia: Hold fast!",
            narration="The storm rises.",
            image_prompt="Warm lantern glow casting directional light on rain-slicked yellow coat",
        )
    ]


def test_painted_page_stage_constraints_exclude_no_shading():
    service, storage, image = make_service()
    draft_pixels = image_bytes("draft-pixels")
    character_pixels = image_bytes("nia-pixels")
    draft_url = storage.put("draft.png", draft_pixels, "image/png")
    char_url = storage.put("nia.png", character_pixels, "image/png")

    url, model_id, provenance, variant = service.generate_painted_page(
        page_id="test-page-1",
        page_number=1,
        panels=sample_panels(),
        style="classic-comic",
        hashed_user_id="user-123",
        variant=VARIANT_DRAFT_CONDITIONED,
        draft_image_url=draft_url,
        character_identities=[
            CharacterRenderIdentity(
                character_id="nia",
                name="Nia",
                description="Keeper",
                visual_attributes={"clothing": "yellow coat"},
                reference_image_url=char_url,
            )
        ],
    )

    assert variant == VARIANT_DRAFT_CONDITIONED
    assert "painted_page_1" in url
    assert model_id != ""
    assert provenance is not None
    assert len(image.recorded_calls) == 1
    call = image.recorded_calls[0]
    prompt = call["prompt"].lower()

    # Stage-specific golden constraints: rich painting, lighting, depth, NO "no shading"
    assert "finished painted comic book page" in prompt
    assert "rich colors" in prompt or "rich color" in prompt
    assert "directional lighting" in prompt
    assert "depth" in prompt
    assert "no shading" not in prompt
    assert "flat white fills" not in prompt
    assert "black and white" not in prompt
    assert "2:3 aspect ratio" in prompt
    assert "never write a speaker's name" in prompt
    assert "Nia: Hold fast!" not in prompt
    assert '"dialogue": "hold fast!"' in prompt

    # Composition reference attached first, character second
    assert call["reference_images"] == [draft_pixels, character_pixels]


def test_painted_page_direct_from_brief_variant_omits_draft():
    service, storage, image = make_service()
    character_pixels = image_bytes("nia-pixels")
    char_url = storage.put("nia.png", character_pixels, "image/png")

    url, model_id, provenance, variant = service.generate_painted_page(
        page_id="test-page-2",
        page_number=2,
        panels=sample_panels(),
        style="classic-comic",
        hashed_user_id="user-123",
        variant=VARIANT_DIRECT_FROM_BRIEF,
        character_identities=[
            CharacterRenderIdentity(
                character_id="nia",
                name="Nia",
                description="Keeper",
                visual_attributes={"clothing": "yellow coat"},
                reference_image_url=char_url,
            )
        ],
    )

    assert variant == VARIANT_DIRECT_FROM_BRIEF
    assert "painted_page_2" in url
    assert model_id != ""
    assert provenance is not None
    assert len(image.recorded_calls) == 1
    call = image.recorded_calls[0]
    prompt = call["prompt"].lower()

    assert "directly from brief" in prompt
    assert "no shading" not in prompt
    assert "flat white fills" not in prompt
    assert "2:3 aspect ratio" in prompt
    assert "never write a speaker's name" in prompt
    assert "Nia: Hold fast!" not in prompt
    assert '"dialogue": "hold fast!"' in prompt

    # Direct-from-brief attaches only character references, NO draft image
    assert call["reference_images"] == [character_pixels]


def test_painted_page_supports_style_reference():
    service, storage, image = make_service()
    style_pixels = image_bytes("style-sample-pixels")
    character_pixels = image_bytes("nia-pixels")
    style_url = storage.put("style.png", style_pixels, "image/png")
    char_url = storage.put("nia.png", character_pixels, "image/png")

    service.generate_painted_page(
        page_id="test-page-3",
        page_number=3,
        panels=sample_panels(),
        style="painterly-watercolor",
        hashed_user_id="user-123",
        variant=VARIANT_DIRECT_FROM_BRIEF,
        character_identities=[
            CharacterRenderIdentity(
                character_id="nia",
                name="Nia",
                description="Keeper",
                visual_attributes={},
                reference_image_url=char_url,
            )
        ],
        style_reference_image_url=style_url,
    )

    call = image.recorded_calls[0]
    assert call["reference_images"] == [character_pixels, style_pixels]
    assert "approved style reference" in call["prompt"]
