"""Conservative, OCR-free removal of isolated margin lettering.

Only works outside confidently enclosed panel borders. See docs/PAGE_ART.md
for thresholds, sample measurements, and deliberate abstentions.
"""

from dataclasses import dataclass
from io import BytesIO
from math import ceil, floor
from statistics import median

from PIL import Image, ImageDraw

Box = tuple[int, int, int, int]


@dataclass(frozen=True)
class PageArtCleanup:
    image_bytes: bytes
    removed_boxes: tuple[Box, ...]
    reason: str


@dataclass(frozen=True)
class _Stroke:
    box: Box
    area: int

    @property
    def width(self):
        return self.box[2] - self.box[0]

    @property
    def height(self):
        return self.box[3] - self.box[1]


def _components(mask: bytes, width: int, height: int) -> list[_Stroke]:
    """Four-connected dark strokes; bounded to a 1024px analysis image."""
    pending = bytearray(mask)
    strokes = []
    for start in range(len(pending)):
        if not pending[start]:
            continue
        pending[start] = 0
        stack = [start]
        left = right = start % width
        top = bottom = start // width
        area = 0
        while stack:
            pos = stack.pop()
            y, x = divmod(pos, width)
            left, right = min(left, x), max(right, x)
            top, bottom = min(top, y), max(bottom, y)
            area += 1
            for neighbor in (
                pos - 1 if x else -1,
                pos + 1 if x + 1 < width else -1,
                pos - width,
                pos + width,
            ):
                if 0 <= neighbor < width * height and pending[neighbor]:
                    pending[neighbor] = 0
                    stack.append(neighbor)
        strokes.append(_Stroke((left, top, right + 1, bottom + 1), area))
    return strokes


def _encloses_panel(stroke: _Stroke, mask: bytes, width: int, height: int) -> bool:
    if stroke.width < width * 0.18 or stroke.height < height * 0.18:
        return False
    left, top, right, bottom = stroke.box
    strip = max(2, round(min(width, height) * 0.008))
    # A large drawing is insufficient: all four rectangle edges must be continuous.
    coverage = (
        sum(any(mask[y * width + x] for y in range(top, top + strip)) for x in range(left, right))
        / stroke.width,
        sum(
            any(mask[y * width + x] for y in range(bottom - strip, bottom))
            for x in range(left, right)
        )
        / stroke.width,
        sum(any(mask[y * width + x] for x in range(left, left + strip)) for y in range(top, bottom))
        / stroke.height,
        sum(
            any(mask[y * width + x] for x in range(right - strip, right))
            for y in range(top, bottom)
        )
        / stroke.height,
    )
    return min(coverage) >= 0.90


def _union(boxes: list[Box]) -> Box:
    return (
        min(b[0] for b in boxes),
        min(b[1] for b in boxes),
        max(b[2] for b in boxes),
        max(b[3] for b in boxes),
    )


def _overlaps(a: Box, b: Box) -> bool:
    return a[0] < b[2] and b[0] < a[2] and a[1] < b[3] and b[1] < a[3]


def _lettering(strokes: list[_Stroke], band: Box, height: int) -> list[Box]:
    """Find repeated glyph heights/baselines and bounded inter-glyph spacing."""
    outside = [
        s
        for s in strokes
        if (
            band[0] <= s.box[0]
            and band[1] <= s.box[1]
            and s.box[2] <= band[2]
            and s.box[3] <= band[3]
        )
    ]
    glyphs = [
        s
        for s in outside
        if (
            max(4, height * 0.008) <= s.height <= height * 0.055
            and 0.12 * s.height <= s.width <= 3 * s.height
            and s.area >= 3
            and s.area / (s.width * s.height) >= 0.08
        )
    ]
    found: set[Box] = set()
    for seed in glyphs:
        aligned = sorted(
            (
                s
                for s in glyphs
                if (
                    0.65 * seed.height <= s.height <= 1.5 * seed.height
                    and abs(s.box[3] - seed.box[3]) <= 0.4 * seed.height
                )
            ),
            key=lambda s: s.box[0],
        )
        runs: list[list[_Stroke]] = [[]]
        for stroke in aligned:
            if runs[-1] and stroke.box[0] - runs[-1][-1].box[2] > 4 * seed.height:
                runs.append([])
            runs[-1].append(stroke)
        for run in runs:
            if len(run) < 5:
                continue
            typical_height = median(s.height for s in run)
            baseline = median(s.box[3] for s in run)
            if not 0.8 * typical_height <= seed.height <= 1.25 * typical_height:
                continue
            if max(s.box[3] for s in run) - min(s.box[3] for s in run) > max(
                2, 0.35 * typical_height
            ):
                continue
            box = _union([s.box for s in run])
            if box[2] - box[0] < 4 * median(s.height for s in run):
                continue
            # Grow a confirmed line through lowercase letters, dots and punctuation.
            # These fragments need not have the cap height used to establish confidence.
            while True:
                neighbors = [
                    s.box
                    for s in outside
                    if (
                        (
                            s.height <= 2.1 * typical_height
                            or (s.height <= 4 * typical_height and s.width <= 2 * typical_height)
                        )
                        and s.width <= 3 * typical_height
                        and box[0] - 3 * typical_height <= s.box[0] <= box[2] + 3 * typical_height
                        and baseline - 2 * typical_height <= s.box[1] < baseline
                    )
                ]
                grown = _union([box, *neighbors])
                if grown == box:
                    break
                box = grown
            pad = max(1, round(seed.height * 0.18))
            box = (
                max(band[0], box[0] - pad),
                max(band[1], box[1] - pad),
                min(band[2], box[2] + pad),
                min(band[3], box[3] + pad),
            )
            # Do not erase a drawing, bubble or border intersecting the text rectangle.
            if any(_overlaps(s.box, box) and s not in outside for s in strokes):
                continue
            if any(
                _overlaps(s.box, box)
                and (
                    (
                        s.height > 2.1 * typical_height
                        and (s.width > 2 * typical_height or s.height > 4 * typical_height)
                    )
                    or s.width > 6 * typical_height
                )
                for s in outside
            ):
                continue
            found.add(box)
    # Include thin editorial underlines immediately below confidently detected text.
    for box in tuple(found):
        line_height = box[3] - box[1]
        rules = [
            s.box
            for s in outside
            if (
                s.height <= max(3, ceil(line_height * 0.2))
                and s.width >= line_height * 0.8
                and box[3] - 2 <= s.box[1] <= box[3] + line_height * 0.4
                and box[0] - line_height * 0.5 <= s.box[0]
                and s.box[2] <= box[2] + line_height * 0.5
            )
        ]
        if rules:
            x0, y0, x1, y1 = _union([box, *rules])
            rule_pad = ceil(line_height * 0.15)
            found.add(
                (
                    max(band[0], x0 - rule_pad),
                    max(band[1], y0 - 1),
                    min(band[2], x1 + rule_pad),
                    min(band[3], y1 + 1),
                )
            )
    # Include antialiasing immediately outside thresholded stroke bounds.
    padded = {
        (
            max(band[0], b[0] - 1),
            max(band[1], b[1] - 1),
            min(band[2], b[2] + 1),
            min(band[3], b[3] + 1),
        )
        for b in found
    }
    return sorted(
        b
        for b in padded
        if not any(
            other != b
            and other[0] <= b[0]
            and other[1] <= b[1]
            and b[2] <= other[2]
            and b[3] <= other[3]
            for other in padded
        )
    )


def clean_page_art(data: bytes) -> PageArtCleanup:
    """Return original bytes on ambiguity, otherwise lossless PNG with local blanks.

    The canvas and panel grid never move or resize. No model, OCR, or network calls.
    """

    def unchanged(reason):
        return PageArtCleanup(data, (), reason)

    try:
        with Image.open(BytesIO(data)) as source:
            if getattr(source, "n_frames", 1) != 1:
                return unchanged("animated-image")
            width, height = source.size
            if min(width, height) < 160 or width * height > 16_000_000:
                return unchanged("unsupported-dimensions")
            if source.mode not in ("RGB", "L", "RGBA", "P"):
                return unchanged("unsupported-paper-mode")
            if source.mode in ("RGBA", "P") and source.convert("RGBA").getchannel(
                "A"
            ).getextrema() != (255, 255):
                return unchanged("transparent-paper")
            image = source.convert("RGB")
    except (OSError, ValueError, Image.DecompressionBombError):
        return unchanged("undecodable-image")

    analysis = image.copy()
    analysis.thumbnail((1024, 1024), Image.Resampling.LANCZOS)
    aw, ah = analysis.size
    gray = analysis.convert("L")
    corners = [
        analysis.getpixel((x, y)) for x, y in ((0, 0), (aw - 1, 0), (0, ah - 1), (aw - 1, ah - 1))
    ]
    if any(min(pixel) < 235 for pixel in corners):
        return unchanged("non-paper-or-full-bleed")
    paper = tuple(round(median(pixel[c] for pixel in corners)) for c in range(3))
    mask = gray.point(lambda value: 1 if value < 160 else 0).tobytes()
    strokes = _components(mask, aw, ah)
    panels = [s.box for s in strokes if _encloses_panel(s, mask, aw, ah)]
    if not panels:
        return unchanged("no-enclosed-panel-grid")
    left, top, right, bottom = _union(panels)
    if right - left < 0.55 * aw or bottom - top < 0.55 * ah:
        return unchanged("small-or-ambiguous-panel-grid")
    guard = max(3, ceil(min(aw, ah) * 0.004))
    if min(left, top, aw - right, ah - bottom) <= guard:
        return unchanged("panel-grid-touches-margin")
    bands = [
        (0, 0, aw, top - guard),
        (0, bottom + guard, aw, ah),
        (0, top, left - guard, bottom),
        (right + guard, top, aw, bottom),
    ]
    boxes = [box for band in bands for box in _lettering(strokes, band, ah)]
    if not boxes:
        return unchanged("no-confident-margin-lettering")
    removed = tuple(
        sorted(
            {
                (
                    max(0, floor(b[0] * width / aw)),
                    max(0, floor(b[1] * height / ah)),
                    min(width, ceil(b[2] * width / aw)),
                    min(height, ceil(b[3] * height / ah)),
                )
                for b in boxes
            }
        )
    )
    draw = ImageDraw.Draw(image)
    for x0, y0, x1, y1 in removed:
        draw.rectangle((x0, y0, x1 - 1, y1 - 1), fill=paper)
    output = BytesIO()
    image.save(output, format="PNG")
    return PageArtCleanup(output.getvalue(), removed, "isolated-margin-lettering")
