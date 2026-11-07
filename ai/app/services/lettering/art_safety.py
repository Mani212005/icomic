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

    def intersects_face(self, box: Box, aspect: float) -> bool:
        page_box = Box(box.x / aspect, box.y, box.width / aspect, box.height)
        return any(page_box.overlaps(face) for face in self.face_boxes)


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
    """Build a coarse busy map and face-like warm-cluster exclusions."""
    with Image.open(BytesIO(image_bytes)) as source:
        image = source.convert("RGB")
        image.thumbnail((480, 720), Image.Resampling.LANCZOS)
        rgb = np.asarray(image, dtype=np.uint8)
    height, width, _ = rgb.shape
    red, green, blue = rgb.astype(np.int16).transpose(2, 0, 1)
    skin = (
        (red > 42)
        & (red - green > 5)
        & (green - blue > 2)
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
        if w < width * 0.025 or h < height * 0.025:
            continue
        if w > width * 0.24 or h > height * 0.20 or not 0.38 <= ratio <= 1.65:
            continue
        # Allow hairline, jaw and small gaps in painted skin clusters.
        pad_x, pad_y = w * 0.20, h * 0.18
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
    high_contrast_subject = (color_range >= 0.35) & (gray <= 0.78)
    dark_saturated_silhouette = (color_range >= 0.25) & (gray <= 0.60)
    busy = (edge_density >= 0.13) | (local_variance >= 0.012) | high_contrast_subject
    integral = np.pad(busy.astype(np.int32), ((1, 0), (1, 0))).cumsum(0).cumsum(1)
    subject_integral = np.pad(dark_saturated_silhouette.astype(np.int32), ((1, 0), (1, 0)))
    subject_integral = subject_integral.cumsum(0).cumsum(1)
    return ArtSafetyMap(width, height, integral, subject_integral, tuple(faces))


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
                    and not safety.intersects_face(candidate, aspect)
                    and safety.subject_fraction(candidate, aspect) <= 0.01
                ):
                    ratio = safety.busy_fraction(candidate, aspect)
                    distance = (candidate.x - preferred.x) ** 2 + (candidate.y - preferred.y) ** 2
                    face_safe.append((distance, y, x, candidate))
                    candidates.append((ratio, distance, y, x, candidate))
                x += step_x
            y += step_y
    calm = [candidate for candidate in candidates if candidate[0] <= 0.20]
    if calm:
        _, _, _, _, box = min(calm, key=lambda item: (item[1], item[0], item[2], item[3]))
        return box, False
    if face_safe:
        _, _, _, box = min(
            face_safe,
            key=lambda item: (safety.busy_fraction(item[3], aspect), item[0], item[1], item[2]),
        )
        return box, True
    return None, True
