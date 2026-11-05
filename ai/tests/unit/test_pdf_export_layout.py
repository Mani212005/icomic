"""Page-art layout regression tests for PDF export."""

import re
import zlib
from hashlib import sha256
from io import BytesIO
from pathlib import Path

import pytest
from PIL import Image

from app.adapters.storage.fake import FakeStorageAdapter
from app.gen.models import PageExportItem
from app.services.pdf import PdfAssemblyService, PdfWriter, _generate_fallback_png, parse_png_to_rgb


@pytest.mark.parametrize("page_size", ["A4", "US_Letter", "Custom_Comic"])
@pytest.mark.parametrize("image_size", [(400, 800), (800, 400)])
def test_page_art_uses_available_area_without_distortion(page_size, image_size):
    width, height = image_size
    image = _generate_fallback_png(width, height)
    pdf = PdfWriter(page_size).build_pdf(
        project_id="layout-test",
        title="Layout",
        include_ai_disclosure=False,
        pages_data=[{"image_bytes": image, "page_number": 1}],
    )
    content = pdf_text(pdf)
    matrix = re.search(r"([\d.]+) 0 0 ([\d.]+) ([\d.]+) ([\d.]+) cm /Im1 Do", content)
    assert matrix, "page image transform is present"
    display_w, display_h = float(matrix[1]), float(matrix[2])
    assert display_w / display_h == pytest.approx(width / height, rel=0.002)
    page_w, page_h = PdfWriter.PAGE_DIMENSIONS[page_size]
    if height > width:
        assert display_h > page_h * 0.75
    else:
        assert display_w > page_w * 0.80


def test_empty_dialogue_does_not_emit_placeholder_section():
    pdf = PdfWriter().build_pdf(
        project_id="empty-dialogue",
        title="No filler",
        include_ai_disclosure=False,
        pages_data=[{"image_bytes": _generate_fallback_png(), "page_number": 1}],
    )
    content = pdf_text(pdf)
    assert "PANEL SCRIPT & DIALOGUE" not in content
    assert "Visual storytelling panel - dialogue integrated in artwork." not in content


@pytest.mark.parametrize("page_size", ["A4", "US_Letter", "Custom_Comic"])
def test_long_cover_title_wraps_before_cover_details(page_size):
    title = "The Extraordinary Adventures of the Clockwork Cartographer Across Seven Kingdoms"
    pdf = PdfWriter(page_size).build_pdf(
        project_id="long-title",
        title=title,
        include_ai_disclosure=True,
        pages_data=[{"image_bytes": _generate_fallback_png(), "page_number": 1}],
    )
    content = pdf_text(pdf)
    title_lines = re.findall(
        r"BT /F2 26 Tf 0\.1 0\.1 0\.15 rg\s+50 [\d.]+ Td \((.*?)\) Tj ET", content
    )
    assert len(title_lines) >= 2
    assert " ".join(title_lines) == title
    assert max(map(len, title_lines)) <= max(
        18, int((PdfWriter.PAGE_DIMENSIONS[page_size][0] - 100) / 14)
    )
    assert "MANDATORY AI-GENERATION DISCLOSURE" in content
    assert "PROJECT & PROVENANCE RECORD" in content


def test_export_keeps_rgb_master_pixels_and_original_attachment_bytes():
    storage = FakeStorageAdapter()
    current_art = _generate_fallback_png(24, 18)
    original_master = _generate_fallback_png(12, 10) + b"raw-master-metadata"
    art_url = storage.put("pages/current.png", current_art, "image/png")
    master_url = storage.put("pages/master.png", original_master, "image/png")
    pdf_url, _, _ = PdfAssemblyService(storage).assemble_pdf(
        project_id="master-integrity",
        title="Master integrity",
        page_size="A4",
        include_ai_disclosure=True,
        pages=[
            PageExportItem(
                page_number=1,
                image_url=art_url,
                original_image_url=master_url,
            )
        ],
    )
    pdf = storage.get(pdf_url)
    offset = pdf.find(original_master)
    assert offset >= 0
    extracted_original = pdf[offset : offset + len(original_master)]
    assert sha256(extracted_original).digest() == sha256(original_master).digest()
    assert current_art not in pdf
    assert original_master in pdf


def test_twelve_page_native_art_export_stays_under_25_mb():
    image = Image.new("RGB", (1280, 1920), "#edf0e8")
    for x in range(0, 1280, 32):
        for y in range(0, 1920, 48):
            image.paste(
                ((x * 3) % 256, (y * 2) % 256, ((x + y) // 4) % 256), (x, y, x + 32, y + 48)
            )
    encoded = BytesIO()
    image.save(encoded, "PNG", optimize=True)
    display = encoded.getvalue()
    original = b"\x89PNG\r\n\x1a\n" + b"raw-original-provenance"
    pages = [
        {
            "page_number": number,
            "image_bytes": display,
            "original_image_bytes": original,
        }
        for number in range(1, 13)
    ]
    pdf = PdfWriter().build_pdf("size-test", "Twelve Page Test", True, pages)
    assert len(pdf) < 25 * 1024 * 1024
    assert pdf.count(original) == 12
    assert len(re.findall(rb"/Type /Page /Parent", pdf)) == 13


def test_cover_uses_first_page_art_and_falls_back_without_an_image():
    cover_art = Path(__file__).parent.parent / "fixtures/pdf_export/comic-01.jpg"
    pdf = PdfWriter().build_pdf(
        "cover-art",
        "The Tide Clock",
        True,
        [{"page_number": 1, "image_bytes": cover_art.read_bytes()}],
    )
    assert b"/CoverIm" in pdf
    assert b"The Tide Clock" in pdf
    assert "MANDATORY AI-GENERATION DISCLOSURE" in pdf_text(pdf)
    assert "PROJECT & PROVENANCE RECORD" in pdf_text(pdf)

    fallback = PdfWriter().build_pdf("cover-no-art", "Short Title", True, [{"page_number": 1}])
    assert "Artwork preview unavailable" in pdf_text(fallback)
    assert "Short Title" in pdf_text(fallback)
    assert "MANDATORY AI-GENERATION DISCLOSURE" in pdf_text(fallback)


def test_cover_and_every_comic_header_use_the_project_title():
    image = Path(__file__).parent.parent / "fixtures/pdf_export/comic-01.jpg"
    title = "The Tide Clock"
    pdf = PdfWriter().build_pdf(
        "title-consistency",
        title,
        True,
        [{"page_number": number, "image_bytes": image.read_bytes()} for number in range(1, 4)],
    )
    content = pdf_text(pdf)
    assert "(The Tide Clock) Tj" in content
    for number in range(1, 4):
        assert f"({title} - Page {number}) Tj" in content


def test_palette_png_renders_with_its_palette_colours():
    image = Image.new("P", (2, 1))
    image.putpalette([220, 30, 40, 20, 80, 210] + [0] * (768 - 6))
    image.putdata([0, 1])
    from io import BytesIO

    encoded = BytesIO()
    image.save(encoded, format="PNG")
    width, height, pixels = parse_png_to_rgb(encoded.getvalue())
    assert (width, height) == (2, 1)
    assert pixels == bytes((220, 30, 40, 20, 80, 210))


def test_long_page_heading_fits_comic_page_width():
    title = "The Extraordinary Adventures of the Clockwork Cartographer Across Seven Kingdoms"
    pdf = PdfWriter("Custom_Comic").build_pdf(
        project_id="long-page-heading",
        title=title,
        include_ai_disclosure=True,
        pages_data=[{"image_bytes": _generate_fallback_png(), "page_number": 1}],
    )
    content = pdf_text(pdf)
    heading = re.search(
        r"BT /F2 ([\d.]+) Tf 0\.2 0\.2 0\.2 rg\s+50 [\d.]+ Td \((.*?)\) Tj ET",
        content,
    )
    assert heading
    font_size = float(heading[1])
    page_heading = heading[2]
    assert page_heading == f"{title} - Page 1"
    assert (
        font_size * len(page_heading) * 0.55 <= PdfWriter.PAGE_DIMENSIONS["Custom_Comic"][0] - 100
    )


def pdf_text(pdf: bytes) -> str:
    streams = re.findall(rb"stream\r?\n(.*?)\r?\nendstream", pdf, re.DOTALL)
    pieces = []
    for stream in streams:
        try:
            pieces.append(zlib.decompress(stream).decode("latin1", "replace"))
        except zlib.error:
            pieces.append(stream.decode("latin1", "replace"))
    for offset in range(len(pdf) - 1):
        if pdf[offset] != 0x78 or pdf[offset + 1] not in (0x01, 0x5E, 0x9C, 0xDA):
            continue
        decoder = zlib.decompressobj()
        try:
            decoded = decoder.decompress(pdf[offset:])
        except zlib.error:
            continue
        text = decoded.decode("latin1", "replace")
        if decoder.eof and text and text not in pieces:
            pieces.append(text)
    return "\n".join(pieces)
