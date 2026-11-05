#!/usr/bin/env python3
"""Review-only Pillow renderer of serialized glyph positions over a copy of real art.

Run with the AI environment (Pillow/fontTools). Does not wrap/shape text or alter
source art. This is not the future browser/PDF renderer.
"""

import argparse
import json
from pathlib import Path

from fontTools.pens.basePen import BasePen
from fontTools.ttLib import TTFont
from PIL import Image, ImageChops, ImageDraw, ImageFilter

FONT_DIR = Path(__file__).resolve().parents[1] / "ai/app/services/lettering/fonts"
FONTS = {
    "comic-neue-v1": "ComicNeue-Regular.ttf",
    "noto-devanagari-v1": "NotoSansDevanagari-Regular.ttf",
}


class GlyphPen(BasePen):
    def __init__(self, glyphs, origin, scale):
        super().__init__(glyphs)
        self.origin, self.scale, self.contours = origin, scale, []

    def point(self, p):
        return self.origin[0] + p[0] * self.scale, self.origin[1] - p[1] * self.scale

    def _moveTo(self, p):
        self.contours.append([self.point(p)])

    def _lineTo(self, p):
        self.contours[-1].append(self.point(p))

    def _curveToOne(self, a, b, c):
        start = self._getCurrentPoint()
        for i in range(1, 17):
            t = i / 16
            p = tuple(
                (1 - t) ** 3 * start[j]
                + 3 * (1 - t) ** 2 * t * a[j]
                + 3 * (1 - t) * t * t * b[j]
                + t**3 * c[j]
                for j in (0, 1)
            )
            self._lineTo(p)

    def _qCurveToOne(self, a, b):
        start = self._getCurrentPoint()
        for i in range(1, 13):
            t = i / 12
            self._lineTo(
                tuple(
                    (1 - t) ** 2 * start[j] + 2 * (1 - t) * t * a[j] + t * t * b[j] for j in (0, 1)
                )
            )

    def _closePath(self):
        pass

    def _endPath(self):
        pass


def render(source, layout, output):
    if source.resolve() == output.resolve():
        raise ValueError("Review output must differ from source art")
    page = Image.open(source).convert("RGB")
    width, height = page.size
    # Supersample shapes and exact glyph outlines, then downsample once.
    factor = 2
    page = page.resize((width * factor, height * factor), Image.Resampling.LANCZOS)
    width, height = page.size
    fonts = {key: TTFont(FONT_DIR / name) for key, name in FONTS.items()}
    stroke = max(1, round(layout.get("stroke_width", 0.0013) * height))
    for bubble in layout["bubbles"]:
        mask = Image.new("L", page.size)
        draw = ImageDraw.Draw(mask)

        def shape(item, draw=draw):
            rect = item["bounds"]
            bounds = (
                rect["x"] * width,
                rect["y"] * height,
                (rect["x"] + rect["width"]) * width,
                (rect["y"] + rect["height"]) * height,
            )
            if item["kind"] == "ellipse":
                draw.ellipse(bounds, fill=255)
            else:
                draw.rounded_rectangle(bounds, radius=item["radius"] * height, fill=255)

        shape(bubble["shape"])
        if bubble["tail"]:
            draw.polygon([(p["x"] * width, p["y"] * height) for p in bubble["tail"]], fill=255)
        for circle in bubble["thought_tail"]:
            shape(circle)
        # Union body and tail before stroking, avoiding a seam at the tail base.
        page.paste("black", mask=mask)
        page.paste("white", mask=mask.filter(ImageFilter.MinFilter(stroke * 2 + 1)))
        for line in bubble["lines"]:
            font = fonts[line["font_id"]]
            glyphs, order = font.getGlyphSet(), font.getGlyphOrder()
            scale = line["font_size"] * height / font["head"].unitsPerEm
            for glyph in line["glyphs"]:
                pen = GlyphPen(glyphs, (glyph["x"] * width, glyph["y"] * height), scale)
                glyphs[order[glyph["glyph_id"]]].draw(pen)
                ink = Image.new("1", page.size)
                for contour in pen.contours:
                    if len(contour) < 3:
                        continue
                    ring = Image.new("1", page.size)
                    ImageDraw.Draw(ring).polygon(contour, fill=1)
                    ink = ImageChops.logical_xor(ink, ring)
                page.paste("black", mask=ink)
    page.resize((width // factor, height // factor), Image.Resampling.LANCZOS).save(output)
    for font in fonts.values():
        font.close()


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("source", type=Path)
    parser.add_argument("layout", type=Path)
    parser.add_argument("output", type=Path)
    args = parser.parse_args()
    render(args.source, json.loads(args.layout.read_text()), args.output)
