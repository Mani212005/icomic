"""Deterministic palette application for the hermetic comic E2E runner."""

from __future__ import annotations

from collections import deque
from io import BytesIO
from typing import Any

from PIL import Image


def colour_enclosed_regions(
    line_art_bytes: bytes,
    palette: dict[str, Any] | list[dict[str, Any]],
    darkness_threshold: int = 60,
) -> bytes:
    """Fill enclosed non-ink regions in scan order using the approved palette."""
    if isinstance(palette, dict):
        colours = [palette[key] for key in sorted(palette)]
    else:
        colours = [item.get("hex_color") for item in palette]
    rgb_colours = [_parse_hex_colour(value) for value in colours]
    rgb_colours = [colour for colour in rgb_colours if colour is not None]
    if not rgb_colours:
        raise ValueError("approved palette contains no valid hex colours")

    with Image.open(BytesIO(line_art_bytes)) as source:
        image = source.convert("RGB")
    width, height = image.size
    pixels = image.load()
    visited = bytearray(width * height)
    region_index = 0

    for start in range(width * height):
        x0, y0 = start % width, start // width
        if visited[start] or _is_ink(pixels[x0, y0], darkness_threshold):
            visited[start] = 1
            continue

        queue = deque([start])
        visited[start] = 1
        region: list[int] = []
        touches_edge = False
        while queue:
            index = queue.popleft()
            x, y = index % width, index // width
            region.append(index)
            if x == 0 or y == 0 or x == width - 1 or y == height - 1:
                touches_edge = True
            if x > 0:
                _visit_pixel(index - 1, x - 1, y, pixels, visited, queue, darkness_threshold)
            if x + 1 < width:
                _visit_pixel(index + 1, x + 1, y, pixels, visited, queue, darkness_threshold)
            if y > 0:
                _visit_pixel(index - width, x, y - 1, pixels, visited, queue, darkness_threshold)
            if y + 1 < height:
                _visit_pixel(index + width, x, y + 1, pixels, visited, queue, darkness_threshold)

        if touches_edge:
            continue
        fill = rgb_colours[region_index % len(rgb_colours)]
        for index in region:
            pixels[index % width, index // width] = fill
        region_index += 1

    output = BytesIO()
    image.save(output, format="PNG")
    return output.getvalue()


def _visit_pixel(index, x, y, pixels, visited, queue, darkness_threshold):
    if visited[index]:
        return
    visited[index] = 1
    if not _is_ink(pixels[x, y], darkness_threshold):
        queue.append(index)


def _is_ink(rgb, threshold):
    return 0.299 * rgb[0] + 0.587 * rgb[1] + 0.114 * rgb[2] < threshold


def _parse_hex_colour(value):
    if not isinstance(value, str) or len(value) != 7 or value[0] != "#":
        return None
    try:
        return tuple(int(value[index : index + 2], 16) for index in (1, 3, 5))
    except ValueError:
        return None
