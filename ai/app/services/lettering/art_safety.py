"""Deterministic raster analysis for keeping automatic lettering off subjects."""

from __future__ import annotations

import math
from collections.abc import Sequence
from dataclasses import dataclass, replace
from io import BytesIO

import numpy as np
from PIL import Image, ImageFilter

from .geometry import Box

# Detected faces at or above this confidence are hard no-go areas for lettering. Lower
# confidence only adds placement cost, so a golden sky mistaken for a face is cheap.
HARD_FACE_CONFIDENCE = 0.85
BIRD_CONFIDENCE = 0.7
# Subject boxes supplied by the vision step (or hand-marked fixtures) are hard no-go areas
# from this confidence upward; weaker boxes only add cost.
HARD_SUBJECT_CONFIDENCE = 0.5
# Breathing room kept around every hard box: detected boxes are never tight, and two sources
# (a vision call and a human) rarely agree to the pixel.
HARD_MARGIN = 0.004
SUBJECT_KINDS = ("face", "hand", "bird", "object")
HARD_KINDS = ("face", "hand", "bird")


@dataclass(frozen=True)
class Subject:
    """A detected face, hand, bird/prop or figure in page-normalized coordinates.

    `character` names the character a face (or hand) belongs to when the vision step could
    tell, so a speech tail can point at its own speaker.
    """

    box: Box
    kind: str
    confidence: float
    character: str | None = None


@dataclass(frozen=True)
class ArtSafetyMap:
    width: int
    height: int
    busy_integral: np.ndarray
    subject_integral: np.ndarray
    gold_integral: np.ndarray
    gold_pixel_integral: np.ndarray
    face_boxes: tuple[Box, ...]
    subjects: tuple[Subject, ...] = ()
    edge_integral: np.ndarray | None = None

    def _fraction(self, integral: np.ndarray, box: Box, aspect: float) -> float:
        x0 = max(0, min(self.width, int(box.x / aspect * self.width)))
        x1 = max(x0 + 1, min(self.width, int(np.ceil(box.right / aspect * self.width))))
        y0 = max(0, min(self.height, int(box.y * self.height)))
        y1 = max(y0 + 1, min(self.height, int(np.ceil(box.bottom * self.height))))
        total = integral[y1, x1] - integral[y0, x1] - integral[y1, x0] + integral[y0, x0]
        return float(total / ((x1 - x0) * (y1 - y0)))

    def busy_fraction(self, box: Box, aspect: float) -> float:
        return self._fraction(self.busy_integral, box, aspect)

    def subject_fraction(self, box: Box, aspect: float) -> float:
        return self._fraction(self.subject_integral, box, aspect)

    def gold_fraction(self, box: Box, aspect: float) -> float:
        return self._fraction(self.gold_integral, box, aspect)

    def gold_pixel_fraction(self, box: Box, aspect: float) -> float:
        return self._fraction(self.gold_pixel_integral, box, aspect)

    def intersects_face(self, box: Box, aspect: float, margin: float = 0.0) -> bool:
        page_box = Box(box.x / aspect, box.y, box.width / aspect, box.height)
        return any(
            page_box.overlaps(
                Box(
                    face.x - margin,
                    face.y - margin,
                    face.width + 2 * margin,
                    face.height + 2 * margin,
                )
            )
            for face in self.face_boxes
        )


def with_subjects(base: ArtSafetyMap | None, subjects: Sequence[Subject]) -> ArtSafetyMap:
    """Replace colour heuristics with explicit subject boxes, keeping detail (edge) scoring.

    Boxes from the vision step replace the face/brass/silhouette heuristics entirely; the
    local detail maps still describe how calm each slot is. Without pixels the map is flat.
    """
    if base is None:
        base = _flat_map()
    zeros = np.zeros_like(base.subject_integral)
    # Faces, hands and birds are never covered. Key objects (lamps, boats) only add cost.
    hard = tuple(
        s.box for s in subjects if s.kind in HARD_KINDS and s.confidence >= HARD_SUBJECT_CONFIDENCE
    )
    return replace(
        base,
        subject_integral=zeros,
        gold_integral=zeros,
        gold_pixel_integral=zeros,
        face_boxes=hard,
        subjects=tuple(subjects),
    )


def _flat_map() -> ArtSafetyMap:
    """A perfectly calm map with no subjects, for pages without art pixels."""
    zeros = np.zeros((73, 49), dtype=np.float64)
    return ArtSafetyMap(48, 72, zeros, zeros, zeros, zeros, (), (), zeros)


def _components(mask: np.ndarray) -> list[tuple[int, int, int, int, int]]:
    """Return 8-connected component bounds and area without an OpenCV dependency."""
    height, width = mask.shape
    seen = np.zeros(mask.shape, dtype=np.bool_)
    components = []
    for y, x in zip(*np.nonzero(mask)):
        if seen[y, x]:
            continue
        stack = [(int(y), int(x))]
        seen[y, x] = True
        min_x = max_x = int(x)
        min_y = max_y = int(y)
        area = 0
        while stack:
            cy, cx = stack.pop()
            area += 1
            min_x, max_x = min(min_x, cx), max(max_x, cx)
            min_y, max_y = min(min_y, cy), max(max_y, cy)
            for ny in range(max(0, cy - 1), min(height, cy + 2)):
                for nx in range(max(0, cx - 1), min(width, cx + 2)):
                    if mask[ny, nx] and not seen[ny, nx]:
                        seen[ny, nx] = True
                        stack.append((ny, nx))
        components.append((min_x, min_y, max_x + 1, max_y + 1, area))
    return components


def _integral(values: np.ndarray) -> np.ndarray:
    return np.pad(values.astype(np.float64), ((1, 0), (1, 0))).cumsum(0).cumsum(1)


def _pixel_mean(integral: np.ndarray, x0: int, y0: int, x1: int, y1: int) -> float:
    x1, y1 = max(x1, x0 + 1), max(y1, y0 + 1)
    total = integral[y1, x1] - integral[y0, x1] - integral[y1, x0] + integral[y0, x0]
    return float(total / ((x1 - x0) * (y1 - y0)))


def _page_mean(integral: np.ndarray, box: Box, width: int, height: int) -> float:
    return _pixel_mean(
        integral,
        max(0, int(box.x * width)),
        max(0, int(box.y * height)),
        min(width, int(np.ceil(box.right * width))),
        min(height, int(np.ceil(box.bottom * height))),
    )


def analyze_art(image_bytes: bytes) -> ArtSafetyMap:
    """Build a calmness map and conservative subject exclusions."""
    with Image.open(BytesIO(image_bytes)) as source:
        image = source.convert("RGB")
        image.thumbnail((480, 720), Image.Resampling.LANCZOS)
        rgb = np.asarray(image, dtype=np.uint8)
    height, width, _ = rgb.shape
    red, green, blue = rgb.astype(np.int16).transpose(2, 0, 1)
    skin = (
        (red > 42)
        & (red - green > 15)
        & (green - blue > 8)
        & (red > green * 1.025)
        & (red - green < 100)
        & (green - blue < 100)
    )
    # Real skin is a saturated orange-brown. Golden skies, sunlit windows and light
    # beams satisfy the loose ordering above but are paler and more yellow, so the
    # share of tightly skin-colored pixels in a box measures how face-like it is.
    hue, saturation, value = np.asarray(Image.fromarray(rgb).convert("HSV")).transpose(2, 0, 1)
    tight = skin & (hue <= 21) & (saturation >= 105) & (value <= 215)
    tight_integral = _integral(tight)
    skin_image = Image.fromarray(skin.astype(np.uint8) * 255).filter(ImageFilter.MaxFilter(7))
    skin_array = np.asarray(skin_image) > 0
    cell = 4
    trim_h, trim_w = height - height % cell, width - width % cell
    skin_cells = (
        skin_array[:trim_h, :trim_w]
        .reshape(trim_h // cell, cell, trim_w // cell, cell)
        .mean(axis=(1, 3))
        >= 0.30
    )
    faces: list[Subject] = []
    subjects: list[Subject] = []

    def page_box(left, top, right, bottom):
        return Box(left, top, right - left, bottom - top)

    def add_face(box):
        faces.append(
            Subject(box, "face", min(1.0, _page_mean(tight_integral, box, width, height) / 0.35))
        )

    for x0, y0, x1, y1, area in _components(skin_cells):
        w, h = (x1 - x0) * cell, (y1 - y0) * cell
        ratio = w / max(h, 1)
        if w > width * 0.24 or h > height * 0.20:
            # Skin-colored pixels can connect from a face through a hand/body.
            # Keep a conservative upper-center head region for that figure.
            component_top = y0 * cell
            face_top = (
                max(0, component_top - h * 0.12) if component_top > height * 0.5 else component_top
            )
            face_left = max(0, x0 * cell + w * 0.25) / width
            face_top /= height
            face_right = min(width, x0 * cell + w * 0.94) / width
            face_bottom = min(height, y0 * cell + h * 0.65) / height
            add_face(Box(face_left, face_top, face_right - face_left, face_bottom - face_top))
            continue
        if w < width * 0.03 or h < height * 0.03:
            continue
        # Reject torso, hand and lighting components. The skin-color mask can
        # span several warm pixels, so only face-sized components become hard
        # exclusions; metallic props are protected by the separate gold mask.
        if w > width * 0.24 or h > height * 0.20 or not 0.38 <= ratio <= 1.65:
            continue
        # Allow a modest jaw/hairline margin. Large padding merged unrelated warm
        # regions across whole panels and falsely marked every top pocket as a face.
        pad_x, pad_y = w * 0.18, h * 0.24
        left = max(0, x0 * cell - pad_x) / width
        top = max(0, y0 * cell - pad_y) / height
        right = min(width, x1 * cell + pad_x) / width
        bottom = min(height, y1 * cell + pad_y) / height
        add_face(Box(left, top, right - left, bottom - top))

    luminance = Image.fromarray(rgb).convert("L")
    edges = np.asarray(luminance.filter(ImageFilter.FIND_EDGES), dtype=np.float32) / 255
    edge_image = Image.fromarray(np.uint8(edges * 255))
    edge_density = np.asarray(edge_image.filter(ImageFilter.BoxBlur(8)), dtype=np.float32) / 255
    gray = np.asarray(luminance, dtype=np.float32) / 255
    mean = np.asarray(luminance.filter(ImageFilter.BoxBlur(8)), dtype=np.float32) / 255
    squared = Image.fromarray(np.uint8(np.clip(gray * gray * 255, 0, 255)))
    mean_square = np.asarray(squared.filter(ImageFilter.BoxBlur(8)), dtype=np.float32) / 255
    local_variance = np.maximum(0, mean_square - mean * mean)
    color_range = (rgb.max(axis=2).astype(np.float32) - rgb.min(axis=2)) / 255
    # Calmness depends only on local detail and variation. Brightness alone must
    # not make a dark, smooth background look busy (or a bright sky look unsafe).
    edge_integral = _integral(edge_density)
    busy = (edge_density >= 0.13) | (local_variance >= 0.012)
    integral = np.pad(busy.astype(np.int32), ((1, 0), (1, 0))).cumsum(0).cumsum(1)

    # Exclude connected warm metallic highlights such as the brass bird and
    # bright props. A dilated mask catches their immediate silhouettes.
    warm_gold = (
        (rgb[:, :, 0] >= 120)
        & (rgb[:, :, 1] >= 90)
        & (rgb[:, :, 0] >= rgb[:, :, 1] * 0.90)
        & (rgb[:, :, 1] > rgb[:, :, 2] * 1.25)
        & (gray >= 0.48)
        & (color_range >= 0.20)
    )
    # Keep coarse component bounds for clear subjects and the local pixel mask
    # for large connected glows. Both must be clear before a bubble is placed.
    gold_image = Image.fromarray(warm_gold.astype(np.uint8) * 255).filter(ImageFilter.MaxFilter(7))
    gold_mask = np.asarray(gold_image) > 0
    gold_subject = np.zeros((height, width), dtype=np.bool_)
    for x0, y0, x1, y1, area in _components(gold_mask):
        box_w, box_h = x1 - x0, y1 - y0
        if (
            width * height * 0.0002 <= area <= width * height * 0.10
            and box_w <= width * 0.35
            and box_h <= height * 0.35
        ):
            if area >= width * height * 0.002:
                # Compact, detailed brass areas are birds or props; confidence is
                # moderate because glints and light beams also pass this test.
                grow_x, grow_y = box_w * 0.12, box_h * 0.12
                subjects.append(
                    Subject(
                        Box(
                            max(0, x0 - grow_x) / width,
                            max(0, y0 - grow_y) / height,
                            (box_w + 2 * grow_x) / width,
                            (box_h + 2 * grow_y) / height,
                        ),
                        "bird",
                        BIRD_CONFIDENCE,
                    )
                )
            margin = max(1, round(12 * width / 1280))
            gold_subject[
                max(0, y0 - margin) : min(height, y1 + margin),
                max(0, x0 - margin) : min(width, x1 + margin),
            ] = True

    # Large coherent edge components approximate figure silhouettes without
    # classifying dark pixels as people. Bounding-box fill protects the interior.
    edge_mask = edge_density >= 0.28
    silhouette = np.zeros((height, width), dtype=np.bool_)
    for x0, y0, x1, y1, area in _components(edge_mask):
        box_w, box_h = x1 - x0, y1 - y0
        bounds_area = box_w * box_h
        if (
            area >= width * height * 0.006
            and bounds_area <= width * height * 0.50
            and box_w >= width * 0.12
            and box_h >= height * 0.12
        ):
            pad_x, pad_y = round(box_w * 0.05), round(box_h * 0.05)
            silhouette[
                max(0, y0 - pad_y) : min(height, y1 + pad_y),
                max(0, x0 - pad_x) : min(width, x1 + pad_x),
            ] = True

    # Bright, chromatic connected highlights protect lanterns and sun glints.
    highlights = (gray >= 0.88) & (color_range >= 0.08)
    highlight_image = Image.fromarray(highlights.astype(np.uint8) * 255).filter(
        ImageFilter.MaxFilter(7)
    )
    highlight_mask = np.asarray(highlight_image) > 0
    highlight_subject = np.zeros((height, width), dtype=np.bool_)
    for x0, y0, x1, y1, area in _components(highlight_mask):
        if width * height * 0.0001 <= area <= width * height * 0.03:
            highlight_subject[y0:y1, x0:x1] = True
    subject = gold_subject | silhouette | highlight_subject
    subject_integral = np.pad(subject.astype(np.int32), ((1, 0), (1, 0)))
    subject_integral = subject_integral.cumsum(0).cumsum(1)
    gold_integral = np.pad(gold_subject.astype(np.int32), ((1, 0), (1, 0)))
    gold_integral = gold_integral.cumsum(0).cumsum(1)
    gold_pixel_integral = np.pad(gold_mask.astype(np.int32), ((1, 0), (1, 0)))
    gold_pixel_integral = gold_pixel_integral.cumsum(0).cumsum(1)
    return ArtSafetyMap(
        width,
        height,
        integral,
        subject_integral,
        gold_integral,
        gold_pixel_integral,
        tuple(f.box for f in faces if f.confidence >= HARD_FACE_CONFIDENCE),
        (*faces, *subjects),
        edge_integral,
    )


# Slot cost weights. Costs are dimensionless sums of area fractions (0-1) and the
# normalized edge density, so one unit is roughly "a slot entirely over detail".
COST_BUSY = 1.0
COST_EDGE = 0.5
COST_SUBJECT = 4.0
COST_GOLD = 0.25
COST_GOLD_GLOW = 0.5
COST_FACE = 3.0
COST_TOP = 8.0
COST_SIDE = 6.0
COST_AREA = 6.0
HALO = 0.02
# Pull toward the preferred position (the speaker's side of the panel for speech).
COST_PREFERRED = 0.5
# A slot that is almost entirely detail (busy fraction above this) is never lettered over,
# whatever else it scores: with every slot like that the line is withheld for review.
MAX_BUSY = 0.95
# A slot costing more than this is not calm enough to letter; the line is withheld
# with a review note rather than drawn over art.
MAX_SLOT_COST = 14.0
# A reserved-zone slot at or below this cost is taken as is. Above it the search widens
# to the whole panel margin and takes the cheapest slot anywhere inside the panel.
ZONE_COST_LIMIT = 1.0
# Cost of using the smallest permitted type instead of the requested size. Type may
# shrink when that buys a calmer slot, but never below the documented 18 pt floor.
SIZE_COST = 1.0


def slot_cost(box: Box, panel: Box, safety: ArtSafetyMap, aspect: float) -> float:
    """Continuous cost of lettering over `box` (physical page-height units)."""
    edge = 0.0
    if safety.edge_integral is not None:
        edge = (
            _page_mean(
                safety.edge_integral,
                Box(box.x / aspect, box.y, box.width / aspect, box.height),
                safety.width,
                safety.height,
            )
            / 0.15
        )
    area = max(box.width * box.height, 1e-12)
    # Subjects are scored against a slightly grown slot so a bubble hugging a face or
    # bird costs more than one with breathing room (detected boxes are never tight).
    halo = Box(box.x - HALO, box.y - HALO, box.width + 2 * HALO, box.height + 2 * HALO)
    covered = 0.0
    for subject in safety.subjects:
        page = Box(
            subject.box.x * aspect, subject.box.y, subject.box.width * aspect, subject.box.height
        )
        ox = min(halo.right, page.right) - max(halo.x, page.x)
        oy = min(halo.bottom, page.bottom) - max(halo.y, page.y)
        if ox > 0 and oy > 0:
            covered += subject.confidence * ox * oy / (halo.width * halo.height)
    top = (box.bottom - panel.y) / max(panel.height, 1e-12)
    side = min(box.x - panel.x, panel.right - box.right) / max(panel.width, 1e-12)
    return (
        COST_BUSY * safety.busy_fraction(box, aspect)
        + COST_EDGE * edge
        + COST_SUBJECT * safety.subject_fraction(halo, aspect)
        + COST_GOLD * safety.gold_fraction(halo, aspect)
        + COST_GOLD_GLOW * safety.gold_pixel_fraction(box, aspect)
        + COST_FACE * min(covered, 1.0)
        + COST_TOP * max(0.0, top)
        + COST_SIDE * max(0.0, side)
        + COST_AREA * area / max(panel.width * panel.height, 1e-12)
    )


def place_in_calm_art(
    size: Box,
    zones: list[Box],
    occupied: list[Box],
    gap: float,
    edge: str | None,
    floor: tuple[float, float],
    preferred: Box,
    safety: ArtSafetyMap,
    aspect: float,
    panel: Box | None = None,
) -> tuple[Box | None, float]:
    """Pick the minimum-cost slot, preferring the reserved zones over the panel margin.

    Hard rules never bend: no overlap with a confident face, another bubble, or the
    panel edge. Everything else is a continuous cost (see `slot_cost`). The reserved
    zones are searched first; only when none is calm enough does the search widen to
    the rest of the panel. `None` means withhold the line for review.
    """
    step_x = max(size.width * 0.16, aspect / safety.width * 8)
    step_y = max(size.height * 0.16, 8 / safety.height)
    inset = 0.004

    def search(regions: list[Box], limit: float) -> tuple[float, Box] | None:
        options: list[tuple[float, float, float, Box]] = []
        for region in regions:
            if region.width < size.width or region.height < size.height:
                continue
            y = region.y
            if edge == "bottom":
                y = region.bottom - size.height
            while y <= region.bottom - size.height + 1e-9:
                xs = []
                x = region.x
                while x <= region.right - size.width + 1e-9:
                    xs.append(x)
                    x += step_x
                # The grid always includes the right-aligned slot, so both sides of a
                # region are equally reachable.
                xs.append(region.right - size.width)
                for x in xs:
                    candidate = Box(x, y, size.width, size.height)
                    if (
                        (y, x) >= floor
                        and not any(candidate.overlaps(b, gap) for b in occupied)
                        and not safety.intersects_face(candidate, aspect, margin=HARD_MARGIN)
                        and safety.busy_fraction(candidate, aspect) <= MAX_BUSY
                    ):
                        cost = slot_cost(candidate, panel or region, safety, aspect)
                        if cost <= limit:
                            options.append((cost, y, x, candidate))
                y += step_y
        if not options:
            return None
        cost, _, _, box = min(
            options,
            key=lambda o: (
                round(
                    o[0] + COST_PREFERRED * math.hypot(o[3].x - preferred.x, o[3].y - preferred.y),
                    6,
                ),
                o[1],
                o[2],
            ),
        )
        return cost, box

    found = search(zones, ZONE_COST_LIMIT)
    if found is None and panel is not None:
        wide = Box(
            panel.x + inset, panel.y + inset, panel.width - 2 * inset, panel.height - 2 * inset
        )
        found = search([wide], MAX_SLOT_COST)
    return (found[1], found[0]) if found else (None, math.inf)
