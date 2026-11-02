"""Unit tests for AI services using dependency injection of fake adapters."""

import pytest

from app.adapters.image.fake import FakeImageAdapter
from app.adapters.language.fake import FakeLanguageAdapter
from app.adapters.storage.fake import FakeStorageAdapter
from app.adapters.vision.fake import FakeVisionAdapter
from app.gen.models import CharacterProfile, PageExportItem, PanelTextItem
from app.services.character import CharacterService
from app.services.colour import ColourService
from app.services.draft import DraftService
from app.services.line_art import LineArtService
from app.services.moderation import ModerationService
from app.services.panel import PanelService
from app.services.pdf import PdfAssemblyService
from app.services.script import ScriptService


def test_script_service_basic():
    lang = FakeLanguageAdapter()
    svc = ScriptService(lang)
    profiles, chapters, word_count, warnings = svc.analyze_script(
        "A detective walks through the rain-soaked streets of Neo-Tokyo looking for answers."
    )
    assert len(profiles) > 0
    assert profiles[0].name == "Protagonist"
    assert chapters >= 1
    assert word_count > 0
    assert "Script is very short" in warnings[0]


def test_script_service_empty_validation():
    lang = FakeLanguageAdapter()
    svc = ScriptService(lang)
    with pytest.raises(ValueError, match="Script input cannot be empty"):
        svc.analyze_script("   \n\t  ")


def test_script_service_long_script_warning():
    lang = FakeLanguageAdapter()
    svc = ScriptService(lang)
    long_script = "word " * 5005
    _, _, word_count, warnings = svc.analyze_script(long_script)
    assert word_count == 5005
    assert any("Script is very long" in w for w in warnings)


def test_character_service():
    img = FakeImageAdapter()
    storage = FakeStorageAdapter()
    svc = CharacterService(img, storage)

    url, model_id, prov = svc.generate_reference_image(
        character_id="char-111",
        character_name="Maya",
        prompt="Cyberpunk artist with teal hair",
        hashed_user_id="user-hash-1",
        style="comic",
    )
    assert url.endswith("characters/char-111/reference.png")
    assert model_id == "muse-image-1.0-fake"
    assert prov["c2pa"]["watermark_present"] is True


def test_panel_service_outline():
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
    chapters = svc.generate_chapter_outline("A great story unfolds.", 3, profiles)
    assert len(chapters) == 3
    assert chapters[0].chapter_number == 1
    assert "Maya" in chapters[0].character_names

    with pytest.raises(ValueError, match="at least 1"):
        svc.generate_chapter_outline("Story", 0, profiles)


def test_panel_service_panel_text():
    lang = FakeLanguageAdapter()
    svc = PanelService(lang)

    panels = svc.generate_panel_text(1, "The Beginning", "An adventure begins", ["Maya"])
    assert len(panels) == 4
    for i, p in enumerate(panels, start=1):
        assert p.panel_number == i
        assert p.image_prompt != ""
        assert p.scene_description != ""


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
        page_id="page-101",
        page_number=1,
        panels=panels,
        style="noir",
        hashed_user_id="hash-1",
    )
    assert url.endswith("pages/page-101/draft_page_1.png")
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
        page_id="page-101",
        page_number=1,
        draft_image_url="http://storage/pages/page-101/draft_page_1.png",
        panels=panels,
        style="noir",
        hashed_user_id="hash-1",
    )
    assert url.endswith("pages/page-101/line_art_page_1.png")
    assert model_id == "muse-image-1.0-fake"
    assert "c2pa" in prov


def test_colour_service():
    vis = FakeVisionAdapter()
    svc = ColourService(vis)
    theme, palette = svc.suggest_palette(b"dummy-line-art", "Alley context")
    assert theme != ""
    assert len(palette) > 0
    assert palette[0].hex_color.startswith("#")


def test_moderation_service():
    lang = FakeLanguageAdapter()
    svc = ModerationService(lang)

    blocked, reason = svc.moderate_prompt("A superhero flying across the sky")
    assert not blocked
    assert reason is None

    blocked_bad, reason_bad = svc.moderate_prompt("An illegal nsfw graphic scene")
    assert blocked_bad
    assert "prohibited" in reason_bad


def test_pdf_service():
    storage = FakeStorageAdapter()
    svc = PdfAssemblyService(storage)

    pages = [
        PageExportItem(
            page_number=1,
            image_url="http://storage/page1.png",
            panel_count=4,
            provenance_metadata={"watermark": True},
        )
    ]
    url, preserved, count = svc.assemble_pdf(
        project_id="proj-999",
        title="Epic Comic",
        page_size="A4",
        include_ai_disclosure=True,
        pages=pages,
    )
    assert url.endswith("exports/proj-999/comic.pdf")
    assert preserved is True
    assert count == 1

    pdf_data = storage.get("exports/proj-999/comic.pdf")
    assert pdf_data.startswith(b"%PDF-1.4")
    assert b"Generated with Icomic AI" in pdf_data


def test_pdf_service_empty_pages():
    storage = FakeStorageAdapter()
    svc = PdfAssemblyService(storage)
    with pytest.raises(ValueError, match="zero pages"):
        svc.assemble_pdf(
            project_id="proj-999",
            title="Empty",
            page_size="A4",
            include_ai_disclosure=True,
            pages=[],
        )
