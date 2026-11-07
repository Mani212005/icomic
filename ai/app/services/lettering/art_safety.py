"""Deterministic raster analysis for keeping automatic lettering off subjects."""

from __future__ import annotations

from dataclasses import dataclass
from io import BytesIO

import numpy as np
from PIL import Image, ImageFilter

from .geometry import Box


@dataclass(frozen=True)
class ArtSafetyMap:
    width: int
    height: int
    busy_integral: np.ndarray
    subject_integral: np.ndarray
    gold_integral: np.ndarray
    gold_pixel_integral: np.ndarray
    face_boxes: tuple[Box, ...]

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
    faces: list[Box] = []
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
            faces.append(Box(face_left, face_top, face_right - face_left, face_bottom - face_top))
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
        faces.append(Box(left, top, right - left, bottom - top))

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
        width, height, integral, subject_integral, gold_integral, gold_pixel_integral, tuple(faces)
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
) -> tuple[Box | None, bool]:
    """Choose a calm reserved slot outside face and subject silhouettes."""
    step_x = max(size.width * 0.16, aspect / safety.width * 8)
    step_y = max(size.height * 0.16, 8 / safety.height)
    candidates: list[tuple[float, float, float, float, Box]] = []
    face_safe: list[tuple[float, float, float, Box]] = []
    for zone in zones:
        if zone.width < size.width or zone.height < size.height:
            continue
        y = zone.y
        if edge == "bottom":
            y = zone.bottom - size.height
        while y <= zone.bottom - size.height + 1e-9:
            x = zone.x
            while x <= zone.right - size.width + 1e-9:
                candidate = Box(x, y, size.width, size.height)
                if (
                    (y, x) >= floor
                    and not any(candidate.overlaps(b, gap) for b in occupied)
                    and not safety.intersects_face(candidate, aspect, margin=0.004)
                    and safety.subject_fraction(candidate, aspect) <= 0.35
                    and safety.gold_fraction(candidate, aspect) == 0.0
                    and safety.gold_pixel_fraction(candidate, aspect) <= 0.02
                ):
                    ratio = safety.busy_fraction(candidate, aspect)
                    distance = (candidate.x - preferred.x) ** 2 + (candidate.y - preferred.y) ** 2
                    face_safe.append((distance, y, x, candidate))
                    candidates.append((ratio, distance, y, x, candidate))
                x += step_x
            y += step_y
    calm = [candidate for candidate in candidates if candidate[0] <= 0.75]
    if calm:
        _, _, _, _, box = min(calm, key=lambda item: (item[0], item[1], item[2], item[3]))
        return box, False
    if face_safe:
        _, _, _, box = min(
            face_safe,
            key=lambda item: (safety.busy_fraction(item[3], aspect), item[0], item[1], item[2]),
        )
        return box, True
    return None, True
