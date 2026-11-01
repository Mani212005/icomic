"""Unit tests for services using dependency injection of fake adapters."""

from app.adapters.image.fake import FakeImageAdapter
from app.adapters.language.fake import FakeLanguageAdapter
from app.adapters.storage.fake import FakeStorageAdapter
from app.adapters.vision.fake import FakeVisionAdapter
from app.gen.models import CharacterProfile, PanelTextItem
from app.services.character import CharacterService
from app.services.colour import ColourService
from app.services.draft import DraftService
from app.services.line_art import LineArtService
from app.services.moderation import ModerationService
from app.services.panel import PanelService
from app.services.script import ScriptService


def test_script_service():
    lang = FakeLanguageAdapter()
    svc = ScriptService(lang)
    profiles, chapters, word_count, warnings = svc.analyze_script(
        "A detective walks through the rain-soaked streets of Neo-Tokyo looking for answers."
    )
    assert len(profiles) > 0
    assert chapters >= 1
    assert word_count > 0
    assert isinstance(warnings, list)


def test_character_service():
    img = FakeImageAdapter()
    storage = FakeStorageAdapter()
    svc = CharacterService(img, storage)

    url, model_id, prov = svc.generate_reference_image(
        character_id="char-1",
        character_name="Maya",
        prompt="Cyberpunk artist with teal hair",
        hashed_user_id="user-hash-1",
        style="comic",
    )
    assert url.endswith("characters/char-1/reference.png")
    assert model_id == "muse-image-1.0-fake"
    assert prov["c2pa"]["watermark_present"] is True


def test_panel_service():
    lang = FakeLanguageAdapter()
    svc = PanelService(lang)

    profiles = [
        CharacterProfile(
            name="Maya",
            description="Artist",
            age_range="20-25",
            appearance="Teal hair",
            hair_style="Pixie",
            clothing="Hoodie",
            personality="Brave",
        )
    ]
    chapters = svc.generate_chapter_outline("A great story unfolds.", 2, profiles)
    assert len(chapters) == 2

    panels = svc.generate_panel_text(1, "The Beginning", "An adventure begins", ["Maya"])
    assert len(panels) == 4
    assert panels[0].image_prompt != ""


def test_draft_service():
    img = FakeImageAdapter()
    storage = FakeStorageAdapter()
    svc = DraftService(img, storage)

    panels = [
        PanelTextItem(
            panel_number=1,
            scene_description="Wide alley",
            character_actions="Stands",
            camera_angle="Wide",
            background="Alley",
            dialogue="Look here",
            narration="It began",
            image_prompt="Panel 1",
        )
    ]
    url, model_id, prov = svc.generate_draft_sketch(
        page_id="page-1",
        page_number=1,
        panels=panels,
        style="noir",
        hashed_user_id="hash-1",
    )
    assert url.endswith("pages/page-1/draft_page_1.png")
    assert model_id == "muse-image-1.0-fake"
    assert "c2pa" in prov


def test_line_art_service():
    img = FakeImageAdapter()
    storage = FakeStorageAdapter()
    svc = LineArtService(img, storage)

    panels = [
        PanelTextItem(
            panel_number=1,
            scene_description="Wide alley",
            character_actions="Stands",
            camera_angle="Wide",
            background="Alley",
            dialogue="Look here",
            narration="It began",
            image_prompt="Panel 1",
        )
    ]
    url, model_id, prov = svc.generate_line_art(
        page_id="page-1",
        page_number=1,
        draft_image_url="http://storage/pages/page-1/draft_page_1.png",
        panels=panels,
        style="noir",
        hashed_user_id="hash-1",
    )
    assert url.endswith("pages/page-1/line_art_page_1.png")
    assert model_id == "muse-image-1.0-fake"
    assert "c2pa" in prov


def test_colour_service():
    vis = FakeVisionAdapter()
    svc = ColourService(vis)
    theme, palette = svc.suggest_palette(b"dummy-line-art", "Alley context")
    assert theme != ""
    assert len(palette) > 0


def test_moderation_service():
    lang = FakeLanguageAdapter()
    svc = ModerationService(lang)

    blocked, reason = svc.moderate_prompt("A superhero flying across the sky")
    assert not blocked
    assert reason is None

    blocked_bad, reason_bad = svc.moderate_prompt("An illegal nsfw graphic scene")
    assert blocked_bad
    assert "prohibited" in reason_bad
