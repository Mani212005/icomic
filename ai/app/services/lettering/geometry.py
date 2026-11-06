"""Small geometry primitives in physical page-height units, with downward Y."""

from dataclasses import dataclass

EPSILON = 1e-9


@dataclass(frozen=True)
class Box:
    x: float
    y: float
    width: float
    height: float

    @property
    def right(self) -> float:
        return self.x + self.width

    @property
    def bottom(self) -> float:
        return self.y + self.height

    @property
    def center(self) -> tuple[float, float]:
        return self.x + self.width / 2, self.y + self.height / 2

    def contains(self, other: "Box") -> bool:
        return (
            self.x - EPSILON <= other.x
            and self.y - EPSILON <= other.y
            and self.right + EPSILON >= other.right
            and self.bottom + EPSILON >= other.bottom
        )

    def overlaps(self, other: "Box", gap: float = 0) -> bool:
        return (
            self.x < other.right + gap - EPSILON
            and self.right + gap > other.x + EPSILON
            and self.y < other.bottom + gap - EPSILON
            and self.bottom + gap > other.y + EPSILON
        )


def polygon_hits_box(points: list[tuple[float, float]], box: Box) -> bool:
    """Convex polygon/rectangle intersection via the separating-axis theorem."""
    if not points:
        return False
    rect = [(box.x, box.y), (box.right, box.y), (box.right, box.bottom), (box.x, box.bottom)]
    axes = [(1.0, 0.0), (0.0, 1.0)]
    for a, b in zip(points, points[1:] + points[:1]):
        axes.append((a[1] - b[1], b[0] - a[0]))
    for ax, ay in axes:
        if abs(ax) + abs(ay) < EPSILON:
            continue
        p = [x * ax + y * ay for x, y in points]
        r = [x * ax + y * ay for x, y in rect]
        if max(p) <= min(r) + EPSILON or max(r) <= min(p) + EPSILON:
            return False
    return True


def place(
    size: Box,
    zones: list[Box],
    occupied: list[Box],
    gap: float,
    edge: str | None,
    floor: tuple[float, float],
) -> Box | None:
    """Push candidates right/down past blockers, taking the first in reading order.

    Candidate coordinates are obstacle boundaries, not a resolution-dependent grid.
    The finite Cartesian frontier also handles overlapping reserved zones.
    """
    candidates = []
    for zone in zones:
        xs = sorted(
            {zone.x, zone.x + (zone.width - size.width) / 2, *(b.right + gap for b in occupied)}
        )
        ys = sorted(
            {
                zone.y + min(gap, max(0, (zone.height - size.height) / 2)),
                *(b.bottom + gap for b in occupied),
            }
        )
        if edge:
            ys = [zone.y if edge == "top" else zone.bottom - size.height]
        for y in ys:
            for x in xs:
                candidate = Box(x, y, size.width, size.height)
                if (
                    (y, x) >= floor
                    and zone.contains(candidate)
                    and not any(candidate.overlaps(b, gap) for b in occupied)
                ):
                    candidates.append(candidate)
    if not candidates:
        return None
    # Prefer a centered inset in the earliest available row. Frontier candidates
    # still permit dense packing when the preferred placement is occupied.
    return min(
        candidates,
        key=lambda b: (b.y, round(min(abs(b.center[0] - z.center[0]) for z in zones), 12), b.x),
    )


def ellipse_contains(ellipse: Box, rect: Box) -> bool:
    """Every corner of a text box must lie inside the convex ellipse."""
    cx, cy = ellipse.center
    return all(
        ((x - cx) / (ellipse.width / 2)) ** 2 + ((y - cy) / (ellipse.height / 2)) ** 2
        <= 1 + EPSILON
        for x in (rect.x, rect.right)
        for y in (rect.y, rect.bottom)
    )
