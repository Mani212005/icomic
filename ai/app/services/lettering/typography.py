"""Local versioned OFL fonts and real HarfBuzz shaping. No platform font lookup."""

from dataclasses import dataclass
from functools import lru_cache
from pathlib import Path
from unicodedata import bidirectional, category

import uharfbuzz as hb
from fontTools.ttLib import TTFont

from .geometry import Box

FONT_DIRECTORY = Path(__file__).with_name("fonts")
FONT_FILES = {
    "comic-neue-v1": "ComicNeue-Regular.ttf",
    "noto-devanagari-v1": "NotoSansDevanagari-Regular.ttf",
}


@dataclass(frozen=True)
class Run:
    # glyph ID, Unicode codepoint cluster index, origin X/Y, advance X/Y
    glyphs: tuple[tuple[int, int, float, float, float, float], ...]
    width: float
    ink: Box


@lru_cache(maxsize=2)
def font(font_id: str) -> tuple[hb.Font, frozenset[int]]:
    path = FONT_DIRECTORY / FONT_FILES[font_id]
    data = path.read_bytes()
    face = hb.Face(data)
    result = hb.Font(face)
    result.scale = (face.upem, face.upem)
    hb.ot_font_set_funcs(result)
    with TTFont(path) as tt:
        cmap = frozenset(tt.getBestCmap())
    return result, cmap


def select_font(text: str) -> tuple[str, list[str]]:
    font_id = (
        "noto-devanagari-v1" if any("\u0900" <= c <= "\u097f" for c in text) else "comic-neue-v1"
    )
    _, cmap = font(font_id)
    flags = []
    if any(bidirectional(c) in {"R", "AL", "RLE", "RLO", "RLI"} for c in text):
        flags.append("unsupported_direction")
    # Joining controls are handled by HarfBuzz; other controls and emoji are refused.
    if any(
        (ord(c) not in cmap and c not in "\n\r\t\u200c\u200d")
        or (category(c).startswith("C") and c not in "\n\r\t\u200c\u200d")
        for c in text
    ):
        flags.append("unsupported_glyph")
    return font_id, flags


def shape(text: str, font_id: str, size: float) -> Run:
    f, _ = font(font_id)
    buf = hb.Buffer()
    buf.add_str(text)
    buf.guess_segment_properties()
    hb.shape(f, buf)
    scale = size / f.face.upem
    x = y = 0.0
    glyphs = []
    left = top = right = bottom = 0.0
    for info, pos in zip(buf.glyph_infos, buf.glyph_positions):
        gx, gy = x + pos.x_offset * scale, y - pos.y_offset * scale
        ax, ay = pos.x_advance * scale, -pos.y_advance * scale
        glyphs.append((info.codepoint, info.cluster, gx, gy, ax, ay))
        ext = f.get_glyph_extents(info.codepoint)
        if ext:
            l, t = gx + ext.x_bearing * scale, gy - ext.y_bearing * scale
            left, top = min(left, l), min(top, t)
            right, bottom = max(right, l + ext.width * scale), max(bottom, t - ext.height * scale)
        x, y = x + ax, y + ay
    right = max(right, x)
    return Run(tuple(glyphs), x, Box(left, top, max(right - left, 1e-9), max(bottom - top, 1e-9)))


def wrap(text: str, font_id: str, size: float, width: float) -> list[str]:
    """Greedy word wrapping preserves hard breaks and never splits shaping clusters.

    Overlong tokens stay whole and receive an overflow flag downstream. Whitespace
    within a paragraph is normalized to one space; source text is never mutated.
    """
    lines = []
    for paragraph in text.split("\n"):
        current = ""
        for word in paragraph.split():
            candidate = f"{current} {word}" if current else word
            if current and shape(candidate, font_id, size).width > width:
                lines.append(current)
                current = word
            else:
                current = candidate
        lines.append(current)
    return lines
