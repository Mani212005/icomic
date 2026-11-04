from __future__ import annotations

from io import BytesIO

from PIL import Image, ImageDraw
from scripts.e2e.colouring import colour_enclosed_regions


def test_runner_applies_approved_palette_to_small_line_art_fixture():
    line_art = Image.new("RGB", (7, 7), "white")
    draw = ImageDraw.Draw(line_art)
    draw.rectangle((0, 0, 6, 6), outline="black", width=1)
    draw.line((3, 1, 3, 5), fill="black", width=1)
    source = BytesIO()
    line_art.save(source, format="PNG")
    line_art_bytes = source.getvalue()

    coloured_bytes = colour_enclosed_regions(
        line_art_bytes,
        {"first": "#FF0000", "second": "#0000FF"},
    )
    with Image.open(BytesIO(coloured_bytes)) as coloured:
        assert coloured.format == "PNG"
        assert coloured.getpixel((1, 1)) == (255, 0, 0)
        assert coloured.getpixel((4, 1)) == (0, 0, 255)
        assert coloured.getpixel((0, 0)) == (0, 0, 0)

    with Image.open(BytesIO(line_art_bytes)) as original:
        assert original.getpixel((1, 1)) == (255, 255, 255)
