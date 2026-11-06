#!/usr/bin/env python3
"""Bundle the immutable layout fonts and glyph outlines for hermetic SVG drawing.

Run with the AI environment. WOFF preserves glyph order and metrics; outlines
come directly from the same TTFs used by HarfBuzz, including composite glyphs.
"""

import json
import shutil
from pathlib import Path

from fontTools.pens.svgPathPen import SVGPathPen
from fontTools.ttLib import TTFont

ROOT = Path(__file__).resolve().parents[1]
SOURCE = ROOT / "ai/app/services/lettering/fonts"
TARGET = ROOT / "frontend/src/lib/lettering/fonts"
STATIC = ROOT / "frontend/static/fonts"
FONTS = {
    "comic-neue-v1": "ComicNeue",
    "noto-devanagari-v1": "NotoSansDevanagari",
}


def bundle():
    TARGET.mkdir(parents=True, exist_ok=True)
    for font_id, name in FONTS.items():
        font = TTFont(SOURCE / f"{name}-Regular.ttf", recalcTimestamp=False)
        glyphs = font.getGlyphSet()
        paths = []
        for glyph in font.getGlyphOrder():
            pen = SVGPathPen(glyphs)
            glyphs[glyph].draw(pen)
            paths.append(pen.getCommands())
        data = {"units_per_em": font["head"].unitsPerEm, "paths": paths}
        if font_id == "comic-neue-v1":
            order = font.getGlyphOrder()
            metrics = {
                "cmap": {
                    str(codepoint): font.getGlyphID(name)
                    for codepoint, name in font.getBestCmap().items()
                },
                "advances": [font["hmtx"].metrics[name][0] for name in order],
                "units_per_em": font["head"].unitsPerEm,
            }
            (TARGET / "mock-metrics.json").write_text(
                json.dumps(metrics, separators=(",", ":")) + "\n"
            )
        (TARGET / f"{font_id}.json").write_text(json.dumps(data, separators=(",", ":")) + "\n")
        font.flavor = "woff"
        font.save(STATIC / f"{font_id}.woff")
        shutil.copyfile(SOURCE / f"{name}-OFL.txt", STATIC / f"{name}-OFL.txt")
        font.close()


if __name__ == "__main__":
    bundle()
