"""Draw stored lettering onto page pixels, for the PNG export.

The counterpart of ``pdf.py``: it consumes the same stored layout (bubble shapes, tails and
shaped glyph origins) and never wraps, shapes or places text. Only the pixels under each
bubble change; the rest of the artwork is returned byte-for-byte as decoded. Glyphs are the
exact outlines of the bundled fonts, filled with the even-odd rule, and shapes are
supersampled so edges are antialiased.
"""

import math
from collections.abc import Iterable

from fontTools.pens.basePen import BasePen
from fontTools.ttLib import TTFont
from PIL import Image, ImageChops, ImageDraw, ImageFilter

from ...gen.models import LetteringBubble, LetteringLayout, LetteringShape
from .typography import FONT_DIRECTORY, FONT_FILES

SUPERSAMPLE = 3
_CURVE_STEPS = 12

Contour = list[tuple[float, float]]


class _OutlinePen(BasePen):
    """Flattens a glyph into polygons in page pixels (origin at the baseline, y down)."""

    def __init__(self, glyph_set, origin: tuple[float, float], scale: float):
        super().__init__(glyph_set)
        self.origin, self.scale = origin, scale
        self.contours: list[Contour] = []

    def _map(self, p):
        return self.origin[0] + p[0] * self.scale, self.origin[1] - p[1] * self.scale

    def _moveTo(self, p):
        self.contours.append([self._map(p)])

    def _lineTo(self, p):
        self.contours[-1].append(self._map(p))

    def _curveToOne(self, a, b, c):
        start = self._getCurrentPoint()
        for i in range(1, _CURVE_STEPS + 1):
            t = i / _CURVE_STEPS
            self._lineTo(
                tuple(
                    (1 - t) ** 3 * start[j]
                    + 3 * (1 - t) ** 2 * t * a[j]
                    + 3 * (1 - t) * t * t * b[j]
                    + t**3 * c[j]
                    for j in (0, 1)
                )
            )

    def _qCurveToOne(self, a, b):
        start = self._getCurrentPoint()
        for i in range(1, _CURVE_STEPS + 1):
            t = i / _CURVE_STEPS
            self._lineTo(
                tuple(
                    (1 - t) ** 2 * start[j] + 2 * (1 - t) * t * a[j] + t * t * b[j] for j in (0, 1)
                )
            )

    def _closePath(self):
        pass

    def _endPath(self):
        pass


def _shape_box(shape: LetteringShape, width: int, height: int) -> tuple[float, float, float, float]:
    b = shape.bounds
    return b.x * width, b.y * height, (b.x + b.width) * width, (b.y + b.height) * height


def _bubble_geometry(
    bubble: LetteringBubble, width: int, height: int, fonts: dict[str, TTFont]
) -> tuple[list[Contour], tuple[float, float, float, float]]:
    """Glyph contours in page pixels and the box (page pixels) every mark of the bubble lies in."""
    xs: list[float] = []
    ys: list[float] = []
    for shape in (bubble.shape, *bubble.thought_tail):
        left, top, right, bottom = _shape_box(shape, width, height)
        xs += [left, right]
        ys += [top, bottom]
    xs += [p.x * width for p in bubble.tail]
    ys += [p.y * height for p in bubble.tail]
    contours: list[Contour] = []
    for line in bubble.lines:
        font = fonts[line.font_id]
        glyphs, order = font.getGlyphSet(), font.getGlyphOrder()
        scale = line.font_size * height / font["head"].unitsPerEm
        for glyph in line.glyphs:
            pen = _OutlinePen(glyphs, (glyph.x * width, glyph.y * height), scale)
            glyphs[order[glyph.glyph_id]].draw(pen)
            contours += [c for c in pen.contours if len(c) >= 3]
    for contour in contours:
        xs += [x for x, _ in contour]
        ys += [y for _, y in contour]
    return contours, (min(xs), min(ys), max(xs), max(ys))


def _draw_shape(draw: ImageDraw.ImageDraw, shape: LetteringShape, page: tuple[int, int], to_canvas):
    width, height = page
    left, top, right, bottom = _shape_box(shape, width, height)
    x0, y0 = to_canvas(left, top)
    x1, y1 = to_canvas(right, bottom)
    if shape.kind == "ellipse":
        draw.ellipse((x0, y0, x1, y1), fill=255)
    else:
        radius = min(shape.radius * height * SUPERSAMPLE, (x1 - x0) / 2, (y1 - y0) / 2)
        draw.rounded_rectangle((x0, y0, x1, y1), radius=radius, fill=255)


def _fill_even_odd(ink: Image.Image, contours: Iterable[Contour], to_canvas) -> None:
    """Fill glyph outlines with the even-odd rule, one contour-sized tile at a time."""
    for contour in contours:
        points = [to_canvas(x, y) for x, y in contour]
        x0 = max(0, math.floor(min(x for x, _ in points)) - 1)
        y0 = max(0, math.floor(min(y for _, y in points)) - 1)
        x1 = min(ink.width, math.ceil(max(x for x, _ in points)) + 2)
        y1 = min(ink.height, math.ceil(max(y for _, y in points)) + 2)
        if x1 <= x0 or y1 <= y0:
            continue
        tile = Image.new("L", (x1 - x0, y1 - y0))
        ImageDraw.Draw(tile).polygon([(x - x0, y - y0) for x, y in points], fill=255)
        ink.paste(ImageChops.difference(ink.crop((x0, y0, x1, y1)), tile), (x0, y0))


def _draw_bubble(
    page: Image.Image, bubble: LetteringBubble, stroke: float, fonts: dict[str, TTFont]
) -> None:
    width, height = page.size
    contours, (left, top, right, bottom) = _bubble_geometry(bubble, width, height, fonts)
    pad = math.ceil(stroke) + 2
    box = (
        max(0, math.floor(left) - pad),
        max(0, math.floor(top) - pad),
        min(width, math.ceil(right) + pad),
        min(height, math.ceil(bottom) + pad),
    )
    if box[2] <= box[0] or box[3] <= box[1]:
        return
    size = (box[2] - box[0], box[3] - box[1])
    big = (size[0] * SUPERSAMPLE, size[1] * SUPERSAMPLE)

    def to_canvas(x: float, y: float) -> tuple[float, float]:
        return (x - box[0]) * SUPERSAMPLE, (y - box[1]) * SUPERSAMPLE

    body = Image.new("L", big)
    painter = ImageDraw.Draw(body)
    _draw_shape(painter, bubble.shape, (width, height), to_canvas)
    if bubble.tail:
        painter.polygon([to_canvas(p.x * width, p.y * height) for p in bubble.tail], fill=255)
    for shape in bubble.thought_tail:
        _draw_shape(painter, shape, (width, height), to_canvas)
    # The stroke is the inward half of a doubled line, so it never grows past the silhouette.
    inset = max(1, round(stroke * SUPERSAMPLE))
    fill = body.filter(ImageFilter.MinFilter(inset * 2 + 1))
    ink = Image.new("L", big)
    _fill_even_odd(ink, contours, to_canvas)

    region = page.crop(box)
    for colour, mask in (("black", body), ("white", fill), ("black", ink)):
        region.paste(colour, mask=mask.resize(size, Image.Resampling.BOX))
    page.paste(region, box)


def draw_lettering(image: Image.Image, layout: LetteringLayout) -> Image.Image:
    """A copy of ``image`` with the stored bubbles and text drawn over it."""
    page = image.convert("RGBA" if "A" in image.getbands() else "RGB")
    stroke = layout.stroke_width * page.height
    fonts = {
        font_id: TTFont(FONT_DIRECTORY / FONT_FILES[font_id])
        for font_id in {line.font_id for bubble in layout.bubbles for line in bubble.lines}
    }
    try:
        for bubble in layout.bubbles:
            _draw_bubble(page, bubble, stroke, fonts)
    finally:
        for font in fonts.values():
            font.close()
    return page
