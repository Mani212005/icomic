"""Regression coverage using an actual provider page, without provider calls."""

import re
from io import BytesIO
from pathlib import Path

import pytest
from PIL import Image, ImageChops, ImageDraw, ImageFont, ImageStat

from app.adapters.image.port import ImageResult
from app.adapters.storage.fake import FakeStorageAdapter
from app.gen.models import PageExportItem
from app.services.draft import DraftService
from app.services.line_art import LineArtService
from app.services.page_art import clean_page_art
from app.services.pdf import PdfAssemblyService

FIXTURE = Path(__file__).parents[1] / "fixtures/page_art/live_margin_annotations.png"


class SampleImageAdapter:
    def __init__(self, data):
        self.result = ImageResult(data, {"c2pa": {"source": "fixture"}}, "fixture", "request")
        self.calls = 0

    def generate(self, **kwargs):
        self.calls += 1
        return self.result


@pytest.mark.parametrize("stage", ["draft", "line_art"])
def test_real_provider_page_is_clean_before_storage_and_export(stage, monkeypatch, caplog):
    from app.services import images

    cleanup_calls = []

    def tracked_cleanup(data):
        cleanup_calls.append(data)
        return clean_page_art(data)

    monkeypatch.setattr(images, "clean_page_art", tracked_cleanup)
    caplog.set_level("INFO", logger="app.services.images")
    source = FIXTURE.read_bytes()
    adapter = SampleImageAdapter(source)
    storage = FakeStorageAdapter()
    draft_url = storage.put("draft.png", source, "image/png")
    if stage == "line_art":
        url, model, provenance = LineArtService(adapter, storage).generate_line_art(
            "sample", 1, draft_url, [], "ink", "fixture-user"
        )
    else:
        url, model, provenance = DraftService(adapter, storage).generate_draft_sketch(
            "sample", 1, [], "ink", "fixture-user"
        )
    original = Image.open(BytesIO(source)).convert("RGB")
    cleaned = Image.open(BytesIO(storage.get(url))).convert("RGB")
    assert cleaned.size == original.size
    # Complete glyph bounds measured on the real heading and each notes line.
    for box in [
        (200, 0, 550, 38),
        (324, 460, 363, 469),
        (345, 473, 703, 485),
        (357, 489, 501, 495),
        (357, 498, 562, 511),
    ]:
        assert min(cleaned.crop(box).convert("L").tobytes()) >= 235
    # All panels, speech bubbles, and the separate margin drawings remain exact.
    assert cleaned.crop((74, 44, 694, 456)).tobytes() == original.crop((74, 44, 694, 456)).tobytes()
    for box in [(75, 470, 120, 510), (265, 470, 295, 500)]:
        assert cleaned.crop(box).tobytes() == original.crop(box).tobytes()
    assert model == "fixture"
    assert provenance == adapter.result.provenance_metadata
    assert adapter.calls == 1
    assert adapter.result.image_bytes == source
    assert storage.get(url.removesuffix(".png") + "_raw.png") == source
    assert "isolated-margin-lettering" in caplog.text

    pdf_url, _, count = PdfAssemblyService(storage).assemble_pdf(
        "fixture-project",
        "Fixture",
        "A4",
        True,
        [PageExportItem(page_number=1, image_url=url, provenance_metadata=provenance)],
    )
    pdf = storage.get(pdf_url)
    assert count == 2
    assert storage.get(url) in pdf  # The source display PNG remains attached unchanged.
    jpeg_streams = re.findall(
        rb"/Filter /DCTDecode /Length \d+ >>\nstream\n(.*?)\nendstream", pdf, re.DOTALL
    )
    assert len(jpeg_streams) == 2  # Cover preview and comic page display.
    encoded_display = Image.open(BytesIO(jpeg_streams[-1])).convert("RGB")
    mean_difference = sum(ImageStat.Stat(ImageChops.difference(cleaned, encoded_display)).mean) / 3
    assert mean_difference < 3
    assert cleanup_calls == [source]  # Export reads the working copy without cleaning again.


def test_only_removed_rectangles_change_and_cleanup_is_idempotent():
    source = FIXTURE.read_bytes()
    result = clean_page_art(source)
    original = Image.open(BytesIO(source)).convert("RGB")
    cleaned = Image.open(BytesIO(result.image_bytes)).convert("RGB")
    difference = ImageChops.difference(original, cleaned)
    for x0, y0, x1, y1 in result.removed_boxes:
        difference.paste((0, 0, 0), (x0, y0, x1, y1))
    assert difference.getbbox() is None
    assert clean_page_art(result.image_bytes).image_bytes == result.image_bytes


def png(image):
    output = BytesIO()
    image.save(output, format="PNG")
    return output.getvalue()


def synthetic_page():
    image = Image.new("RGB", (768, 768), "white")
    draw = ImageDraw.Draw(image)
    for box in [
        (60, 100, 370, 380),
        (400, 100, 708, 380),
        (60, 410, 370, 668),
        (400, 410, 708, 668),
    ]:
        draw.rectangle(box, outline="black", width=5)
    return image


@pytest.mark.parametrize(
    "case",
    [
        "no_text",
        "panel_text",
        "touching_border",
        "full_bleed",
        "margin_drawing",
        "bubble_crosses_border",
    ],
)
def test_art_and_panel_dialogue_are_untouched(case):
    image = synthetic_page()
    draw = ImageDraw.Draw(image)
    font = ImageFont.load_default(size=18)
    if case == "panel_text":
        draw.ellipse((90, 130, 340, 220), outline="black", width=3)
        draw.text((110, 155), "Keep this dialogue!", font=font, fill="black")
    elif case == "touching_border":
        draw.rectangle((0, 0, 767, 767), outline="black", width=12)
        draw.text((100, 45), "PAGE 1 FINAL NOTES", font=font, fill="black")
    elif case == "full_bleed":
        draw.rectangle((0, 0, 767, 767), fill=(80, 100, 120))
        draw.text((100, 45), "PAGE 1 FINAL NOTES", font=font, fill="black")
    elif case == "margin_drawing":
        draw.ellipse((75, 20, 135, 80), outline="black", width=3)
        draw.line((85, 65, 120, 30, 110, 60), fill="black", width=3)
        draw.polygon((400, 25, 430, 70, 380, 70), outline="black", width=3)
    elif case == "bubble_crosses_border":
        draw.ellipse((100, 20, 420, 160), fill="white", outline="black", width=3)
        draw.text((140, 45), "Keep this dialogue!", font=font, fill="black")
    source = png(image)
    result = clean_page_art(source)
    assert result.image_bytes == source
    assert not result.removed_boxes
    assert result.reason != "isolated-margin-lettering"


@pytest.mark.parametrize("position", ["top", "bottom", "left", "right"])
def test_isolated_margin_text_is_removed_on_each_side(position):
    image = synthetic_page()
    if position in ("left", "right"):
        # Give a horizontal margin caption enough room without rotating lettering.
        image = image.resize((1000, 768))
        canvas = Image.new("RGB", (1400, 768), "white")
        canvas.paste(image, (200, 0))
        image = canvas
    location = {"top": (100, 40), "bottom": (100, 710), "left": (15, 200), "right": (1240, 200)}[
        position
    ]
    ImageDraw.Draw(image).text(
        location, "FINAL NOTES", font=ImageFont.load_default(size=16), fill="black"
    )
    result = clean_page_art(png(image))
    assert result.removed_boxes
    cleaned = Image.open(BytesIO(result.image_bytes))
    x, y = location
    assert min(cleaned.crop((x, y, x + 120, y + 20)).convert("L").tobytes()) == 255


def test_noop_preserves_provider_bytes_and_records_reason(caplog):
    from app.services.images import store_page_image

    caplog.set_level("INFO", logger="app.services.images")
    source = png(synthetic_page()) + b"untouched-provider-provenance"
    adapter = SampleImageAdapter(source)
    storage = FakeStorageAdapter()
    url = store_page_image(storage, "pages/example/line_art_page_1", adapter.result)
    assert storage.get(url) == source
    assert len(storage._blobs) == 1
    assert "no-confident-margin-lettering" in caplog.text


@pytest.mark.parametrize(
    "source,reason",
    [
        (b"not-an-image", "undecodable-image"),
        (png(Image.new("RGB", (64, 64), "white")), "unsupported-dimensions"),
        (png(Image.new("RGBA", (200, 200))), "transparent-paper"),
    ],
)
def test_unsupported_input_is_preserved(source, reason):
    result = clean_page_art(source)
    assert result.image_bytes == source
    assert result.reason == reason


@pytest.mark.parametrize(
    "format,mode,extension",
    [("JPEG", "RGB", "jpg"), ("WEBP", "RGB", "webp"), ("PNG", "RGBA", "png"), ("PNG", "P", "png")],
)
def test_encoded_sources_survive_lossless_working_copy(format, mode, extension):
    from app.services.images import store_page_image

    image = synthetic_page()
    ImageDraw.Draw(image).text(
        (100, 40), "FINAL NOTES", font=ImageFont.load_default(size=16), fill="black"
    )
    output = BytesIO()
    image.convert(mode).save(output, format=format)
    source = output.getvalue()
    storage = FakeStorageAdapter()
    adapter = SampleImageAdapter(source)
    url = store_page_image(storage, "pages/sample/line_art_page_1", adapter.result)
    assert url.endswith(".png")
    assert storage.get(f"pages/sample/line_art_page_1_raw.{extension}") == source
    original = Image.open(BytesIO(source)).convert("RGB")
    cleaned = Image.open(BytesIO(storage.get(url))).convert("RGB")
    assert min(cleaned.crop((100, 43, 205, 58)).convert("L").tobytes()) == 255
    assert cleaned.crop((55, 95, 714, 674)).tobytes() == original.crop((55, 95, 714, 674)).tobytes()


@pytest.mark.parametrize("scale", [1.5, 2.5])
def test_sample_detection_scales_without_resampling_art(scale):
    image = Image.open(FIXTURE).convert("RGB")
    image = image.resize((round(image.width * scale), round(image.height * scale)))
    result = clean_page_art(png(image))
    cleaned = Image.open(BytesIO(result.image_bytes))
    assert result.removed_boxes
    panel_grid = tuple(round(v * scale) for v in (74, 44, 694, 456))
    assert cleaned.crop(panel_grid).tobytes() == image.crop(panel_grid).tobytes()
    for box in [
        (205, 7, 530, 31),
        (324, 460, 363, 469),
        (345, 473, 703, 485),
        (357, 489, 501, 495),
        (357, 498, 562, 511),
    ]:
        scaled = tuple(round(v * scale) for v in box)
        assert min(cleaned.crop(scaled).convert("L").tobytes()) >= 235
