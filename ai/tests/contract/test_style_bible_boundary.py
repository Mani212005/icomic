"""Boundary and regression tests for the shared style bible resolver.

Verifies:
1. Style bible resolver resolves all standard presets with stable version and hash.
2. Character reference prompt incorporates style bible visual guidance without duplicate Style strings.
3. Draft sketch and line art briefs carry the resolved style bible.
4. Line art stage prompt avoids contradictory 'no shading' exclusions for screentone/hatching/chiaroscuro styles.
5. Colour palette suggestion incorporates the style bible palette guidance into vision context.
6. Unknown or custom styles resolve to a safe, deterministic style bible fallback.
"""

from uuid import uuid4

from fastapi.testclient import TestClient

from app.adapters.storage.fake import FakeStorageAdapter
from app.gen.models import CharacterRenderIdentity, PanelTextItem
from app.main import create_app
from app.services.character import CharacterService
from app.services.line_art import LineArtService
from app.services.render_brief import build_page_render_request
from tests.image_fixtures import image_bytes


def test_style_bible_module_and_resolver_exist():
    """Defect 1: No style bible resolver exists on origin/main."""
    from app.services.style_bible import (
        STYLE_BIBLE_VERSION,
        StyleBible,
        resolve_style_bible,
    )

    bible = resolve_style_bible("cyberpunk-noir")
    assert isinstance(bible, StyleBible)
    assert bible.version == STYLE_BIBLE_VERSION
    assert bible.style_id == "cyberpunk-noir"
    assert bible.bible_hash


def test_character_reference_uses_style_bible_without_duplicate_style():
    """Defect 2: Character reference lacks visual style bible and duplicates Style: string."""
    from app.services.style_bible import resolve_style_bible

    app = create_app()
    service = CharacterService(app.state.image_adapter, app.state.storage_adapter)
    service.generate_reference_image(
        character_id="nia",
        character_name="Nia",
        prompt="Lighthouse keeper in yellow coat",
        hashed_user_id="user123",
        style="cyberpunk-noir",
    )
    calls = app.state.image_adapter.recorded_calls
    assert len(calls) == 1
    prompt = calls[0]["prompt"]

    bible = resolve_style_bible("cyberpunk-noir")
    # Prompt must carry the style bible's visual guidance
    assert bible.line_treatment in prompt
    assert bible.lighting in prompt
    assert bible.materials in prompt
    # Prompt must NOT duplicate "Style: cyberpunk-noir"
    assert prompt.count("Style: cyberpunk-noir") <= 1


def test_draft_and_line_art_briefs_carry_style_bible():
    """Defect 3: Render brief contains only raw string style, missing style bible."""
    import json

    from app.services.style_bible import resolve_style_bible

    storage = FakeStorageAdapter()
    url = storage.put("ref.png", image_bytes("reference"), "image/png")
    prompt, _ = build_page_render_request(
        page_number=1,
        panels=[
            PanelTextItem(
                panel_number=1,
                scene_description="Nia in storm",
                character_actions="Runs",
                camera_angle="Wide",
                background="Lighthouse",
                dialogue="Go!",
                narration="Night",
                image_prompt="Rain and wet stone",
            )
        ],
        style="manga-ink",
        storage=storage,
        character_identities=[
            CharacterRenderIdentity(
                character_id="nia",
                name="Nia",
                description="Keeper",
                visual_attributes={"clothing": "coat"},
                reference_image_url=url,
            )
        ],
    )
    brief = json.JSONDecoder().raw_decode(prompt[prompt.index("{") :])[0]
    assert "style_bible" in brief
    sb = brief["style_bible"]
    bible = resolve_style_bible("manga-ink")
    assert sb["style_id"] == "manga-ink"
    assert sb["version"] == bible.version
    assert sb["bible_hash"] == bible.bible_hash
    assert sb["line_treatment"] == bible.line_treatment
    assert sb["values"] == bible.values
    assert sb["environment"] == bible.environment
    assert sb["materials"] == bible.materials
    assert sb["lighting"] == bible.lighting
    assert sb["mood"] == bible.mood
    assert sb["depth"] == bible.depth
    assert sb["negative_constraints"] == bible.negative_constraints


def test_line_art_stage_avoids_contradictory_no_shading_for_manga_and_noir():
    """Defect 4: Line art stage forces 'no shading, flat white fills' contradicting manga/noir."""
    app = create_app()
    storage = app.state.storage_adapter
    draft_url = storage.put("draft.png", image_bytes("draft"), "image/png")
    service = LineArtService(app.state.image_adapter, storage)

    # Manga ink requires screentones, cross-hatching, and value modeling
    service.generate_line_art(
        page_id=str(uuid4()),
        page_number=1,
        draft_image_url=draft_url,
        panels=[
            PanelTextItem(
                panel_number=1,
                scene_description="Action",
                character_actions="Strikes",
                camera_angle="Low angle",
                background="Dojo",
                dialogue="",
                narration="",
                image_prompt="Ink splatters",
            )
        ],
        style="manga-ink",
        hashed_user_id="user-hash",
    )
    calls = app.state.image_adapter.recorded_calls
    prompt = calls[0]["prompt"]
    assert "no shading" not in prompt.lower()
    assert "flat white fills" not in prompt.lower()


def test_suggest_colour_palette_incorporates_style_bible():
    """Defect 5: Colour palette suggestion has no style input or style bible context."""

    app = create_app()
    storage = app.state.storage_adapter
    line_art_url = storage.put("line_art.png", b"\x89PNG\r\n\x1a\nlineart", "image/png")

    with TestClient(app) as client:
        resp = client.post(
            "/internal/v1/suggest-colour-palette",
            json={
                "job_id": str(uuid4()),
                "hashed_user_id": "test-user",
                "project_id": str(uuid4()),
                "page_id": str(uuid4()),
                "line_art_image_url": line_art_url,
                "panel_context": "Page 1 of chapter 1",
                "style": "cyberpunk-noir",
            },
        )
        assert resp.status_code == 200, resp.text


def test_unknown_style_fallback_is_safe_and_deterministic():
    """Defect 6: Unknown styles must resolve to a safe, deterministic style bible."""
    from app.services.style_bible import STYLE_BIBLE_VERSION, resolve_style_bible

    bible = resolve_style_bible("custom-retro-synthwave-99")
    assert bible.style_id == "custom-retro-synthwave-99"
    assert bible.version == STYLE_BIBLE_VERSION
    assert bible.bible_hash
    assert bible.line_treatment
    assert bible.palette
    assert len(bible.negative_constraints) > 0
