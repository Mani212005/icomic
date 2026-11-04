"""Unit tests for AI services using dependency injection of fake adapters."""

import re
import zlib
from typing import Any

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


def test_script_service_long_script_is_supported():
    lang = FakeLanguageAdapter()
    svc = ScriptService(lang)
    long_script = "word " * 5005
    _, _, word_count, warnings = svc.analyze_script(long_script)
    assert word_count == 5005
    assert warnings == []


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
            visual_attributes={},
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


def parse_pdf_structure(pdf_bytes: bytes) -> dict[str, Any]:
    """Parse raw PDF bytes and extract structural markers, objects, images, and text."""
    info: dict[str, Any] = {
        "header": "",
        "page_count": 0,
        "objects": {},
        "images": [],
        "text_content": "",
        "has_xref": False,
        "has_trailer": False,
        "has_eof": False,
    }
    if pdf_bytes.startswith(b"%PDF-"):
        first_line = pdf_bytes.split(b"\n", 1)[0]
        info["header"] = first_line.decode("latin1", "replace")
    if b"%%EOF" in pdf_bytes:
        info["has_eof"] = True
    if b"xref" in pdf_bytes:
        info["has_xref"] = True
    if b"trailer" in pdf_bytes:
        info["has_trailer"] = True

    pattern = re.compile(rb"(\d+)\s+0\s+obj(.*?)endobj", re.DOTALL)
    for m in pattern.finditer(pdf_bytes):
        obj_num = int(m.group(1))
        content = m.group(2).strip()
        info["objects"][obj_num] = content
        if b"/Type /Pages" in content and b"/Count" in content:
            count_match = re.search(rb"/Count\s+(\d+)", content)
            if count_match:
                info["page_count"] = int(count_match.group(1))
        if b"/Subtype /Image" in content:
            w_match = re.search(rb"/Width\s+(\d+)", content)
            h_match = re.search(rb"/Height\s+(\d+)", content)
            w = int(w_match.group(1)) if w_match else 0
            h = int(h_match.group(1)) if h_match else 0
            info["images"].append({"obj": obj_num, "width": w, "height": h})

    stream_pattern = re.compile(rb"stream\r?\n(.*?)\r?\nendstream", re.DOTALL)
    text_pieces = []
    for m in stream_pattern.finditer(pdf_bytes):
        raw_stream = m.group(1)
        try:
            decomp = zlib.decompress(raw_stream)
            text_pieces.append(decomp.decode("latin1", "replace"))
        except zlib.error:
            text_pieces.append(raw_stream.decode("latin1", "replace"))
    info["text_content"] = "\n".join(text_pieces)
    return info


def test_draft_service_without_character_references():
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
    assert len(img.recorded_calls) == 1
    assert len(img.recorded_calls[0]["reference_images"]) == 0


def test_draft_service_character_consistency_anchoring():
    img = FakeImageAdapter()
    storage = FakeStorageAdapter()
    svc = DraftService(img, storage)

    char_bytes_1 = b"\x89PNG\r\n\x1a\nchar-1-bytes"
    char_bytes_2 = b"\x89PNG\r\n\x1a\nchar-2-bytes"
    url1 = storage.put("characters/char-1/reference.png", char_bytes_1, "image/png")
    url2 = storage.put("characters/char-2/reference.png", char_bytes_2, "image/png")

    panels = [
        PanelTextItem(
            panel_number=1,
            scene_description="Rooftop confrontation",
            character_actions="Maya readies her gear",
            camera_angle="Close-up",
            background="Neo-Tokyo Skyline",
            dialogue="No turning back.",
            narration="",
            image_prompt="Maya on rooftop",
        )
    ]

    url, model_id, prov = svc.generate_draft_sketch(
        page_id="page-202",
        page_number=2,
        panels=panels,
        style="cyberpunk",
        hashed_user_id="hash-user-42",
        character_reference_image_urls=[url1, url2],
    )
    assert url.endswith("pages/page-202/draft_page_2.png")
    assert model_id == "muse-image-1.0-fake"
    assert "c2pa" in prov
    assert len(img.recorded_calls) == 1
    call = img.recorded_calls[0]

    # Verify approved character reference image bytes passed as reference inputs
    assert len(call["reference_images"]) == 2
    assert call["reference_images"][0] == char_bytes_1
    assert call["reference_images"][1] == char_bytes_2

    # Verify approved character reference URLs included in prompt
    assert url1 in call["prompt"]
    assert url2 in call["prompt"]
    assert "Character references:" in call["prompt"]

    # Verify adapter aliases
    assert img.calls == img.recorded_calls
    assert img.recorded_inputs == img.recorded_calls


def test_line_art_service_without_character_references():
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
    draft_bytes = b"\x89PNG\r\n\x1a\ndraft"
    storage.put("pages/page-101/draft_page_1.png", draft_bytes, "image/png")
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
    assert len(img.recorded_calls) == 1
    assert img.recorded_calls[0]["reference_images"] == [draft_bytes]


def test_line_art_service_character_consistency_anchoring():
    img = FakeImageAdapter()
    storage = FakeStorageAdapter()
    svc = LineArtService(img, storage)

    char_bytes = b"\x89PNG\r\n\x1a\nleo-ref-bytes"
    char_url = storage.put("characters/leo/reference.png", char_bytes, "image/png")
    draft_url = "http://localhost:8000/storage/fake/pages/page-303/draft_page_1.png"
    draft_bytes = b"\x89PNG\r\n\x1a\ndraft"
    storage.put(draft_url, draft_bytes, "image/png")

    panels = [
        PanelTextItem(
            panel_number=1,
            scene_description="Lab interior",
            character_actions="Leo examines the device",
            camera_angle="Medium",
            background="Workbench with monitors",
            dialogue="The signal is stabilizing.",
            narration="",
            image_prompt="Leo working in tech lab",
        )
    ]

    url, model_id, prov = svc.generate_line_art(
        page_id="page-303",
        page_number=1,
        draft_image_url=draft_url,
        panels=panels,
        style="comic-ink",
        hashed_user_id="hash-user-leo",
        character_reference_image_urls=[char_url],
    )
    assert url.endswith("pages/page-303/line_art_page_1.png")
    assert model_id == "muse-image-1.0-fake"
    assert "c2pa" in prov
    assert len(img.recorded_calls) == 1
    call = img.recorded_calls[0]

    # Verify approved character reference image bytes passed as reference inputs
    assert call["reference_images"] == [draft_bytes, char_bytes]

    # Verify approved character reference URLs and draft URL in prompt
    assert char_url in call["prompt"]
    assert draft_url in call["prompt"]
    assert "Character references:" in call["prompt"]


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


def test_pdf_service_valid_multipage_and_structural_validation():
    img_adapter = FakeImageAdapter()
    storage = FakeStorageAdapter()
    svc = PdfAssemblyService(storage)

    # Put real PNG images into storage
    png_page_1 = img_adapter.generate("p1", [], "u1", "noir").image_bytes
    png_page_2 = img_adapter.generate("p2", [], "u2", "noir").image_bytes
    url_p1 = storage.put("pages/p1/page_1.png", png_page_1, "image/png")
    url_p2 = storage.put("pages/p2/page_2.png", png_page_2, "image/png")

    pages = [
        PageExportItem(
            page_number=1,
            image_url=url_p1,
            panel_count=2,
            provenance_metadata={
                "c2pa": {
                    "claim_generator": "Meta-AI-Provenance/1.0",
                    "signature": "sig-page-1",
                    "watermark_present": True,
                },
                "model": "muse-image-1.0",
            },
            panels=[
                PanelTextItem(
                    panel_number=1,
                    scene_description="Night alley",
                    character_actions="Maya runs",
                    camera_angle="Wide",
                    background="Wet pavement",
                    dialogue="Keep moving, they are close!",
                    narration="The pursuit had begun.",
                    image_prompt="Panel 1 action",
                ),
                PanelTextItem(
                    panel_number=2,
                    scene_description="Dead end",
                    character_actions="Turns around",
                    camera_angle="Close",
                    background="Brick wall",
                    dialogue="Nowhere left to hide.",
                    narration="",
                    image_prompt="Panel 2 suspense",
                ),
            ],
        ),
        PageExportItem(
            page_number=2,
            image_url=url_p2,
            panel_count=1,
            provenance_metadata={
                "c2pa": {
                    "claim_generator": "Meta-AI-Provenance/1.0",
                    "signature": "sig-page-2",
                    "watermark_present": True,
                },
                "model": "muse-image-1.0",
            },
            panels=[
                PanelTextItem(
                    panel_number=1,
                    scene_description="Helicopter searchlight",
                    character_actions="Looks up",
                    camera_angle="High",
                    background="Beams of light",
                    dialogue="Look up!",
                    narration="Escape arrived from above.",
                    image_prompt="Panel 3 rescue",
                )
            ],
        ),
    ]

    url, preserved, count = svc.assemble_pdf(
        project_id="proj-comic-777",
        title="Shadows of Neo-Tokyo",
        page_size="A4",
        include_ai_disclosure=True,
        pages=pages,
    )
    assert url.endswith("exports/proj-comic-777/comic.pdf")
    assert preserved is True
    # 1 cover page + 2 comic pages = 3 pages
    assert count == 3

    pdf_bytes = storage.get("exports/proj-comic-777/comic.pdf")
    parsed = parse_pdf_structure(pdf_bytes)

    # 1. Structural validity
    assert parsed["header"] == "%PDF-1.4"
    assert parsed["has_eof"] is True
    assert parsed["has_xref"] is True
    assert parsed["has_trailer"] is True

    # 2. Page count in /Pages tree
    assert parsed["page_count"] == 3

    # 3. Embedded image XObjects
    assert len(parsed["images"]) == 2
    for img in parsed["images"]:
        assert img["width"] == 64
        assert img["height"] == 64

    # 4. Mandatory AI disclosure text present
    assert "MANDATORY AI-GENERATION DISCLOSURE" in parsed["text_content"]
    assert "Notice: This comic book was created with Icomic AI" in parsed["text_content"]
    assert "Meta C2PA provenance metadata" in parsed["text_content"]

    # 5. Title & panel dialogue text present
    assert "Shadows of Neo-Tokyo" in parsed["text_content"]
    assert "Keep moving, they are close!" in parsed["text_content"]
    assert "The pursuit had begun." in parsed["text_content"]
    assert "Escape arrived from above." in parsed["text_content"]

    # 6. Provenance metadata in XMP packet
    assert "Meta-AI-Provenance/1.0" in parsed["text_content"]
    assert "sig-page-1" in parsed["text_content"]
    assert "sig-page-2" in parsed["text_content"]


def test_pdf_service_page_sizes_and_fallback_image():
    storage = FakeStorageAdapter()
    svc = PdfAssemblyService(storage)

    pages = [
        PageExportItem(
            page_number=1,
            image_url="http://storage/non-existent-image.png",
            dialogue_text="Stand back, hero!\nI will handle this.",
        )
    ]
    url, preserved, count = svc.assemble_pdf(
        project_id="proj-sizes",
        title="Letter Sized Comic",
        page_size="US_Letter",
        include_ai_disclosure=False,
        pages=pages,
    )
    assert url.endswith("exports/proj-sizes/comic.pdf")
    assert count == 2
    assert preserved is True

    pdf_bytes = storage.get("exports/proj-sizes/comic.pdf")
    parsed = parse_pdf_structure(pdf_bytes)
    assert parsed["page_count"] == 2
    assert len(parsed["images"]) == 1
    assert "Stand back, hero!" in parsed["text_content"]


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


def test_pdf_service_native_jpeg_embedding():
    storage = FakeStorageAdapter()
    svc = PdfAssemblyService(storage)

    # Minimal valid 1x1 grayscale JPEG binary fixture
    tiny_jpeg = bytes(
        [
            0xFF,
            0xD8,
            0xFF,
            0xE0,
            0x00,
            0x10,
            0x4A,
            0x46,
            0x49,
            0x46,
            0x00,
            0x01,
            0x01,
            0x00,
            0x00,
            0x01,
            0x00,
            0x01,
            0x00,
            0x00,
            0xFF,
            0xDB,
            0x00,
            0x43,
            0x00,
            0x03,
            0x02,
            0x02,
            0x03,
            0x02,
            0x02,
            0x03,
            0x03,
            0x03,
            0x03,
            0x04,
            0x03,
            0x03,
            0x04,
            0x05,
            0x08,
            0x05,
            0x05,
            0x04,
            0x04,
            0x05,
            0x0A,
            0x07,
            0x07,
            0x06,
            0x08,
            0x0C,
            0x0A,
            0x0C,
            0x0C,
            0x0B,
            0x0A,
            0x0B,
            0x0B,
            0x0D,
            0x0E,
            0x12,
            0x10,
            0x0D,
            0x0E,
            0x11,
            0x0E,
            0x0B,
            0x0B,
            0x10,
            0x16,
            0x10,
            0x11,
            0x13,
            0x14,
            0x15,
            0x15,
            0x15,
            0x0C,
            0x0F,
            0x17,
            0x18,
            0x16,
            0x14,
            0x18,
            0x12,
            0x14,
            0x15,
            0x14,
            0xFF,
            0xC0,
            0x00,
            0x0B,
            0x08,
            0x00,
            0x01,
            0x00,
            0x01,
            0x01,
            0x01,
            0x11,
            0x00,
            0xFF,
            0xC4,
            0x00,
            0x1F,
            0x00,
            0x00,
            0x01,
            0x05,
            0x01,
            0x01,
            0x01,
            0x01,
            0x01,
            0x01,
            0x00,
            0x00,
            0x00,
            0x00,
            0x00,
            0x00,
            0x00,
            0x00,
            0x01,
            0x02,
            0x03,
            0x04,
            0x05,
            0x06,
            0x07,
            0x08,
            0x09,
            0x0A,
            0x0B,
            0xFF,
            0xC4,
            0x00,
            0xB5,
            0x10,
            0x00,
            0x02,
            0x01,
            0x03,
            0x03,
            0x02,
            0x04,
            0x03,
            0x05,
            0x05,
            0x04,
            0x04,
            0x00,
            0x00,
            0x01,
            0x7D,
            0x01,
            0x02,
            0x03,
            0x00,
            0x04,
            0x11,
            0x05,
            0x12,
            0x21,
            0x31,
            0x41,
            0x06,
            0x13,
            0x51,
            0x61,
            0x07,
            0x22,
            0x71,
            0x14,
            0x32,
            0x81,
            0x91,
            0xA1,
            0x08,
            0x23,
            0x42,
            0xB1,
            0xC1,
            0x15,
            0x52,
            0xD1,
            0xF0,
            0x24,
            0x33,
            0x62,
            0x72,
            0x82,
            0x09,
            0x0A,
            0x16,
            0x17,
            0x18,
            0x19,
            0x1A,
            0x25,
            0x26,
            0x27,
            0x28,
            0x29,
            0x2A,
            0x34,
            0x35,
            0x36,
            0x37,
            0x38,
            0x39,
            0x3A,
            0x43,
            0x44,
            0x45,
            0x46,
            0x47,
            0x48,
            0x49,
            0x4A,
            0x53,
            0x54,
            0x55,
            0x56,
            0x57,
            0x58,
            0x59,
            0x5A,
            0x63,
            0x64,
            0x65,
            0x66,
            0x67,
            0x68,
            0x69,
            0x6A,
            0x73,
            0x74,
            0x75,
            0x76,
            0x77,
            0x78,
            0x79,
            0x7A,
            0x83,
            0x84,
            0x85,
            0x86,
            0x87,
            0x88,
            0x89,
            0x8A,
            0x92,
            0x93,
            0x94,
            0x95,
            0x96,
            0x97,
            0x98,
            0x99,
            0x9A,
            0xA2,
            0xA3,
            0xA4,
            0xA5,
            0xA6,
            0xA7,
            0xA8,
            0xA9,
            0xAA,
            0xB2,
            0xB3,
            0xB4,
            0xB5,
            0xB6,
            0xB7,
            0xB8,
            0xB9,
            0xBA,
            0xC2,
            0xC3,
            0xC4,
            0xC5,
            0xC6,
            0xC7,
            0xC8,
            0xC9,
            0xCA,
            0xD2,
            0xD3,
            0xD4,
            0xD5,
            0xD6,
            0xD7,
            0xD8,
            0xD9,
            0xDA,
            0xE1,
            0xE2,
            0xE3,
            0xE4,
            0xE5,
            0xE6,
            0xE7,
            0xE8,
            0xE9,
            0xEA,
            0xF1,
            0xF2,
            0xF3,
            0xF4,
            0xF5,
            0xF6,
            0xF7,
            0xF8,
            0xF9,
            0xFA,
            0xFF,
            0xDA,
            0x00,
            0x08,
            0x01,
            0x01,
            0x00,
            0x00,
            0x3F,
            0x00,
            0xBF,
            0x00,
            0xFF,
            0xD9,
        ]
    )

    jpeg_url = storage.put("pages/p1/page_1.jpg", tiny_jpeg, "image/jpeg")
    pages = [
        PageExportItem(
            page_number=1,
            image_url=jpeg_url,
            dialogue_text="Native JPEG test page.",
        )
    ]

    _url, preserved, count = svc.assemble_pdf(
        project_id="proj-jpeg",
        title="JPEG Comic",
        page_size="A4",
        include_ai_disclosure=True,
        pages=pages,
    )

    pdf_bytes = storage.get("exports/proj-jpeg/comic.pdf")
    assert b"/Filter /DCTDecode" in pdf_bytes
    assert tiny_jpeg in pdf_bytes  # Raw JPEG bytes preserved untouched
    assert count == 2
    assert preserved is True


def test_pdf_service_invalid_webp_raises():
    storage = FakeStorageAdapter()
    svc = PdfAssemblyService(storage)

    # Valid WebP header
    webp_bytes = b"RIFF\x20\x00\x00\x00WEBPVP8 \x04\x00\x00\x00\x00\x00\x00\x00"
    webp_url = storage.put("pages/p1/page_1.webp", webp_bytes, "image/webp")

    pages = [
        PageExportItem(
            page_number=1,
            image_url=webp_url,
            dialogue_text="WebP comic page.",
        )
    ]

    with pytest.raises(OSError):
        svc.assemble_pdf(
            project_id="proj-webp",
            title="WebP Comic",
            page_size="A4",
            include_ai_disclosure=True,
            pages=pages,
        )
