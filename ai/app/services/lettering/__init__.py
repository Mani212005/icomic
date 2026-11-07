"""Deterministic, opt-in vector lettering. Never accepts or modifies page pixels."""

import itertools
import json
import math
from collections.abc import Sequence
from dataclasses import dataclass
from hashlib import sha256

from ...gen.models import (
    LetteringBubble,
    LetteringDocument,
    LetteringGlyph,
    LetteringIssue,
    LetteringLayout,
    LetteringPanel,
    LetteringPoint,
    LetteringRect,
    LetteringShape,
    LetteringTextLine,
    LetteringUtterance,
)
from .art_safety import (
    HARD_MARGIN,
    SIZE_COST,
    ArtSafetyMap,
    Subject,
    analyze_art,
    place_in_calm_art,
    slot_cost,
    with_subjects,
)
from .geometry import Box, ellipse_contains, place, polygon_hits_box
from .typography import Run, select_font, shape, wrap

# Reserved zones are a hint about where the art left calm space, never the whole panel.
ZONE_MAX_HEIGHT = 0.35
# Lines placed by the fallback chain (split caption or margin strip) are not bound to a zone.
STRIP_INSET = 0.004
# A fallback slot is reviewed only when the art under it is detail from edge to edge.
FALLBACK_MAX_BUSY = 0.995


@dataclass(frozen=True)
class LayoutConfig:
    minimum_font_size: float = 18 / 841.89
    font_size: float = 35 / 841.89
    font_size_step: float = 2 / 841.89
    padding: float = 0.006
    gap: float = 0.012

    def __post_init__(self):
        values = (
            self.minimum_font_size,
            self.font_size,
            self.font_size_step,
            self.padding,
            self.gap,
        )
        if (
            not all(math.isfinite(v) and v > 0 for v in values)
            or self.minimum_font_size < 18 / 841.89
            or self.font_size < self.minimum_font_size
        ):
            raise ValueError("Invalid print size, padding or gap")


@dataclass
class Draft:
    utterance: LetteringUtterance
    box: Box
    font_id: str
    size: float
    runs: list[tuple[str, Run]]
    step: float
    flags: list[str]
    cost: float = 0.0
    fallback: bool = False


def _local_rect(rect, panel: Box) -> Box:
    return Box(
        panel.x + rect.x * panel.width,
        panel.y + rect.y * panel.height,
        rect.width * panel.width,
        rect.height * panel.height,
    )


def _local_point(point, panel: Box) -> tuple[float, float]:
    return panel.x + point.x * panel.width, panel.y + point.y * panel.height


def _rect(box: Box, aspect: float) -> LetteringRect:
    return LetteringRect(x=box.x / aspect, y=box.y, width=box.width / aspect, height=box.height)


def _point(x: float, y: float, aspect: float) -> LetteringPoint:
    return LetteringPoint(x=x / aspect, y=y)


def _validate(document: LetteringDocument) -> None:
    # Pydantic's number fields also allow NaN/Infinity; wire documents must not.
    json.dumps(document.model_dump(mode="json"), allow_nan=False)
    panel_ids, panel_orders, utterance_ids = set(), set(), set()
    unit = Box(0, 0, 1, 1)
    for panel in document.panels:
        if panel.panel_id in panel_ids or panel.order in panel_orders:
            raise ValueError("Panel IDs and orders must be unique")
        panel_ids.add(panel.panel_id)
        panel_orders.add(panel.order)
        for rect in [panel.rect, *panel.reserved_zones]:
            if not unit.contains(Box(rect.x, rect.y, rect.width, rect.height)):
                raise ValueError(
                    "Panel rectangle or reserved zone extends beyond its coordinate space"
                )
        orders = set()
        for utterance in panel.utterances:
            if utterance.utterance_id in utterance_ids or utterance.order in orders:
                raise ValueError("Utterance IDs must be page-unique and orders panel-unique")
            utterance_ids.add(utterance.utterance_id)
            orders.add(utterance.order)


def _draft(
    u: LetteringUtterance,
    panel: Box,
    zones: list[Box],
    cfg: LayoutConfig,
    font_size: float | None = None,
    max_width_fraction: float | None = None,
) -> Draft:
    font_id, flags = select_font(u.text)
    requested = cfg.font_size * (u.placement.size_scale or 1) if u.placement else cfg.font_size
    size = max(cfg.minimum_font_size, font_size if font_size is not None else requested)
    if requested < cfg.minimum_font_size:
        flags.append("below_minimum")
    ellipse = u.kind in {"speech", "thought"}
    factor = math.sqrt(2) if ellipse else 1
    height_factor = 1.5 if ellipse else 1
    widest = max((z.width for z in zones), default=panel.width)
    width = min(
        widest,
        math.sqrt(max(shape(u.text, font_id, size).width, size) * size * 3) * factor
        + cfg.padding * 2,
    )
    if u.kind in {"narration", "caption"}:
        width = min(widest, panel.width * (max_width_fraction or 0.62))
    elif u.kind == "speech":
        width = min(widest, panel.width * (max_width_fraction or 0.38))
    if u.placement and u.placement.rect:
        width = u.placement.rect.width * panel.width
    available = max(size, width / factor - 2 * cfg.padding)
    runs = [(line, shape(line, font_id, size)) for line in wrap(u.text, font_id, size, available)]
    step = max(size * 1.35, max(r.ink.height for _, r in runs) + size * 0.15)
    top = min(r.ink.y + i * step for i, (_, r) in enumerate(runs))
    bottom = max(r.ink.bottom + i * step for i, (_, r) in enumerate(runs))
    height = (bottom - top + 2 * cfg.padding) * height_factor
    if u.kind in {"narration", "caption"} and not (u.placement and u.placement.rect):
        width = min(width, max(r.ink.width for _, r in runs) + 2 * cfg.padding)
    box = Box(0, 0, width, height)
    if u.placement:
        if u.placement.rect:
            box = _local_rect(u.placement.rect, panel)
        elif u.placement.center:
            x, y = _local_point(u.placement.center, panel)
            box = Box(x - width / 2, y - height / 2, width, height)
    return Draft(u, box, font_id, size, runs, step, flags)


def _font_sizes(u: LetteringUtterance, cfg: LayoutConfig) -> list[float]:
    requested = cfg.font_size * (u.placement.size_scale or 1) if u.placement else cfg.font_size
    requested = max(cfg.minimum_font_size, requested)
    if u.placement and u.placement.rect:
        return [requested]
    sizes = []
    size = requested
    while size > cfg.minimum_font_size + 1e-9:
        sizes.append(size)
        size = max(cfg.minimum_font_size, size - cfg.font_size_step)
    sizes.append(cfg.minimum_font_size)
    return sizes


def _draft_candidates(
    utterance: LetteringUtterance,
    panel: Box,
    zones: list[Box],
    config: LayoutConfig,
) -> list[Draft]:
    """Try readable sizes from requested down to the documented print floor."""
    if utterance.kind in {"narration", "caption"}:
        widths = (0.62, 0.50, 0.40, 0.32)
    elif utterance.kind == "speech":
        widths = (0.62, 0.55, 0.48, 0.38)
    else:
        widths = (None,)
    return [
        _draft(utterance, panel, zones, config, size, width)
        for size in _font_sizes(utterance, config)
        for width in widths
    ]


def _lines(d: Draft, aspect: float) -> tuple[list[LetteringTextLine], list[Box]]:
    top = min(r.ink.y + i * d.step for i, (_, r) in enumerate(d.runs))
    bottom = max(r.ink.bottom + i * d.step for i, (_, r) in enumerate(d.runs))
    start = d.box.y + (d.box.height - (bottom - top)) / 2 - top
    lines, boxes = [], []
    for i, (text, run) in enumerate(d.runs):
        x = d.box.x + (d.box.width - run.ink.width) / 2 - run.ink.x
        y = start + i * d.step
        ink = Box(x + run.ink.x, y + run.ink.y, run.ink.width, run.ink.height)
        boxes.append(ink)
        lines.append(
            LetteringTextLine(
                text=text,
                font_id=d.font_id,
                font_size=d.size,
                baseline=_point(x, y, aspect),
                bounds=_rect(ink, aspect),
                glyphs=[
                    LetteringGlyph(
                        glyph_id=g,
                        cluster=c,
                        x=(x + gx) / aspect,
                        y=y + gy,
                        advance_x=ax / aspect,
                        advance_y=ay,
                    )
                    for g, c, gx, gy, ax, ay in run.glyphs
                ],
            )
        )
    return lines, boxes


def _text_fits(draft: Draft, aspect: float, padding: float) -> bool:
    _, ink_boxes = _lines(draft, aspect)
    inner = Box(
        draft.box.x + padding,
        draft.box.y + padding,
        max(1e-9, draft.box.width - 2 * padding),
        max(1e-9, draft.box.height - 2 * padding),
    )
    return all(inner.contains(box) for box in ink_boxes) and (
        draft.utterance.kind not in {"speech", "thought"}
        or all(ellipse_contains(draft.box, box) for box in ink_boxes)
    )


def _tail(
    d: Draft,
    panel: Box,
    texts: list[Box],
    inferred_anchor: tuple[float, float] | None = None,
    reach: float = 0.025,
) -> tuple[list[tuple[float, float]], list[Box]]:
    u, b = d.utterance, d.box
    if u.kind in {"narration", "caption"}:
        return [], []
    anchor = (
        u.placement.tail_anchor
        if u.placement and u.placement.tail_anchor
        else (u.speaker_anchor or inferred_anchor)
    )
    cx, _cy = b.center
    if anchor:
        if isinstance(anchor, tuple):
            ax, ay = panel.x + anchor[0] * panel.width, panel.y + anchor[1] * panel.height
        else:
            ax, ay = _local_point(anchor, panel)
    else:
        # Unknown speakers get a short downward cue, never a needle to the frame.
        ax, ay = cx + b.width * 0.15, min(panel.bottom, b.bottom + 0.025)
    edges = [
        (b.x, min(b.bottom - b.height / 4, max(b.y + b.height / 4, ay))),
        (b.right, min(b.bottom - b.height / 4, max(b.y + b.height / 4, ay))),
        (min(b.right - b.width / 4, max(b.x + b.width / 4, ax)), b.y),
        (min(b.right - b.width / 4, max(b.x + b.width / 4, ax)), b.bottom),
    ]
    index = min(range(4), key=lambda i: ((edges[i][0] - ax) ** 2 + (edges[i][1] - ay) ** 2, i))
    ex, ey = edges[index]
    distance = math.hypot(ax - ex, ay - ey)
    if b.x <= ax <= b.right and b.y <= ay <= b.bottom:
        return [], []
    if u.kind in {"speech", "thought"}:
        cy = b.center[1]
        direction_x, direction_y = ax - cx, ay - cy
        divisor = math.hypot(direction_x / (b.width / 2), direction_y / (b.height / 2))
        ex, ey = cx + direction_x / divisor, cy + direction_y / divisor
        distance = math.hypot(ax - ex, ay - ey)
    manual = u.placement and u.placement.tail_anchor
    if distance > reach and not manual:
        ax, ay = ex + (ax - ex) * reach / distance, ey + (ay - ey) * reach / distance
    half = min(0.008, (b.height if index < 2 else b.width) / 5)
    nx, ny = (0, half) if index < 2 else (half, 0)
    if u.kind in {"speech", "thought"}:
        angle = math.atan2((ey - b.center[1]) / (b.height / 2), (ex - cx) / (b.width / 2))
        tangent_length = math.hypot(b.width / 2 * math.sin(angle), b.height / 2 * math.cos(angle))
        delta = half / tangent_length
        base_a = (
            cx + b.width / 2 * math.cos(angle - delta),
            b.center[1] + b.height / 2 * math.sin(angle - delta),
        )
        base_b = (
            cx + b.width / 2 * math.cos(angle + delta),
            b.center[1] + b.height / 2 * math.sin(angle + delta),
        )
    else:
        base_a, base_b = (ex - nx, ey - ny), (ex + nx, ey + ny)
    # Sample two quadratic edges into the existing vector polygon contract.
    # A wide base and gently bent spine make a short tapered comic tail.
    dx, dy = ax - ex, ay - ey
    bend = min(0.004, math.hypot(dx, dy) * 0.15)
    length = math.hypot(dx, dy) or 1
    qx, qy = (ex + ax) / 2 - dy / length * bend, (ey + ay) / 2 + dx / length * bend

    for scale in [1] if manual else [1, 0.7, 0.4]:
        tip_x, tip_y = ex + dx * scale, ey + dy * scale
        control_x, control_y = ex + (qx - ex) * scale, ey + (qy - ey) * scale

        def curve(sx, sy, t, tx=tip_x, ty=tip_y, qx=control_x, qy=control_y):
            return (
                (1 - t) ** 2 * sx + 2 * (1 - t) * t * qx + t * t * tx,
                (1 - t) ** 2 * sy + 2 * (1 - t) * t * qy + t * t * ty,
            )

        triangle = [curve(*base_a, t) for t in (0, 0.25, 0.5, 0.75, 1)]
        triangle += [curve(*base_b, t) for t in (0.75, 0.5, 0.25, 0)]
        if not any(polygon_hits_box(triangle, box) for box in texts):
            ax, ay = tip_x, tip_y
            break
    else:
        d.flags.append("tail_crosses_text")
        return [], []
    if u.kind == "thought":
        circles = []
        distance = math.hypot(ax - ex, ay - ey)
        for fraction, radius in [(0.25, 0.003), (0.55, 0.002), (1.0, 0.001)]:
            x, y = ex + (ax - ex) * fraction, ey + (ay - ey) * fraction
            if fraction == 1 and distance:
                # The final circle is tangent to the exact requested anchor.
                x -= (ax - ex) / distance * radius
                y -= (ay - ey) / distance * radius
            circles.append(Box(x - radius, y - radius, radius * 2, radius * 2))
        if any(c.overlaps(t) for c in circles for t in texts):
            d.flags.append("tail_crosses_text")
            return [], []
        return [], circles
    return triangle, []


def _name_tokens(name: str | None) -> set[str]:
    return {t for t in "".join(c if c.isalnum() else " " for c in (name or "").casefold()).split()}


def same_character(a: str | None, b: str | None) -> bool:
    """Whether two character names or ids denote the same character (case/punctuation-blind)."""
    left, right = _name_tokens(a), _name_tokens(b)
    return bool(left and right and (left <= right or right <= left))


def _anchor_beside(face: Box, bubble: Box, aspect: float, panel: Box) -> tuple[float, float]:
    """A panel-local tail target just outside the speaker's face box, toward the bubble."""
    x0, y0, x1, y1 = face.x * aspect, face.y, face.right * aspect, face.bottom
    cx, cy = bubble.center
    nx, ny = min(max(cx, x0), x1), min(max(cy, y0), y1)
    dx, dy = cx - nx, cy - ny
    length = math.hypot(dx, dy)
    if length > 1e-9:
        nx, ny = nx + dx / length * 0.004, ny + dy / length * 0.004
    return (nx - panel.x) / panel.width, (ny - panel.y) / panel.height


def _split_caption(u: LetteringUtterance) -> list[LetteringUtterance]:
    words = u.text.split()
    if len(words) < 4:
        return []
    mid = len(words) // 2
    return [
        u.model_copy(update={"utterance_id": f"{u.utterance_id}~{i}", "text": " ".join(part)})
        for i, part in enumerate((words[:mid], words[mid:]), start=1)
    ]


def _fallback_drafts(
    u: LetteringUtterance,
    panel: Box,
    cfg: LayoutConfig,
    aspect: float,
    blockers: list[Box],
    safety: ArtSafetyMap,
    hard: list[Box],
) -> list[Draft]:
    """Place a line that found no calm slot: split caption, then a margin strip.

    Shrinking to the documented minimum already happened in the normal search. Nothing is
    trimmed. The result is never empty unless the panel cannot hold the text at all.
    """
    wide = Box(panel.x, panel.y, panel.width, panel.height)
    if u.kind in {"narration", "caption"}:
        parts = _split_caption(u)
        placed: list[Draft] = []
        occupied = blockers[:]
        for part in parts:
            best = None
            for candidate in _draft_candidates(part, panel, [wide], cfg):
                box, cost = place_in_calm_art(
                    candidate.box,
                    [],
                    occupied,
                    cfg.gap,
                    None,
                    (-math.inf, -math.inf),
                    Box(panel.x, panel.y, candidate.box.width, candidate.box.height),
                    safety,
                    aspect,
                    panel,
                )
                if box is None:
                    continue
                candidate.box = box
                if not _text_fits(candidate, aspect, cfg.padding):
                    continue
                if best is None or cost < best.cost:
                    candidate.cost = cost
                    best = candidate
            if best is None:
                placed = []
                break
            best.fallback = True
            placed.append(best)
            occupied.append(best.box)
        if parts and len(placed) == len(parts):
            return placed
    return _strip_draft(u, panel, cfg, aspect, blockers, safety, hard)


def _strip_draft(
    u: LetteringUtterance,
    panel: Box,
    cfg: LayoutConfig,
    aspect: float,
    blockers: list[Box],
    safety: ArtSafetyMap,
    hard: list[Box],
) -> list[Draft]:
    """Last resort: a margin strip (captions) or the least-bad free slot, never over a subject.

    Captions try the panel's top and bottom edges first. Speech, and captions whose strips are
    blocked, scan a grid over the whole panel. The least overlap with any subject wins, then
    cost; only a slot that still touches a subject is flagged for review.
    """
    options: list[tuple[float, float, float, float, Box, Draft]] = []
    is_caption = u.kind in {"narration", "caption"}
    for strips_only in (True, False) if is_caption else (False,):
        for candidate in _draft_candidates(u, panel, [panel], cfg):
            if not _text_fits(candidate, aspect, cfg.padding):
                continue
            b = candidate.box
            if b.width > panel.width or b.height > panel.height:
                continue
            xs = [panel.x + STRIP_INSET, panel.center[0] - b.width / 2, panel.right - b.width]
            ys = [panel.y + STRIP_INSET, panel.bottom - b.height - STRIP_INSET]
            if not strips_only:
                step_x, step_y = max(b.width * 0.2, 0.01), max(b.height * 0.25, 0.01)
                xs += [
                    panel.x + i * step_x for i in range(int((panel.width - b.width) / step_x) + 1)
                ]
                ys += [
                    panel.y + i * step_y for i in range(int((panel.height - b.height) / step_y) + 1)
                ]
            for y in ys:
                for x in xs:
                    slot = Box(x, y, b.width, b.height)
                    if not panel.contains(slot) or any(slot.overlaps(o, cfg.gap) for o in blockers):
                        continue
                    covered = sum(
                        max(0.0, min(slot.right, h.right) - max(slot.x, h.x))
                        * max(0.0, min(slot.bottom, h.bottom) - max(slot.y, h.y))
                        for h in hard
                    )
                    cost = slot_cost(slot, panel, safety, aspect)
                    shortfall = max(0.0, cfg.font_size - candidate.size) / cfg.font_size
                    if safety.busy_fraction(slot, aspect) > FALLBACK_MAX_BUSY:
                        # Lettering over solid detail is placed, never dropped, but reviewed.
                        covered += 1e-6
                    options.append(
                        (
                            round(covered, 9),
                            round(cost + SIZE_COST * shortfall, 6),
                            y,
                            x,
                            slot,
                            candidate,
                        )
                    )
        if options and min(o[0] for o in options) == 0:
            break
    if not options:
        return []
    covered, _, _, _, slot, draft = min(options, key=lambda o: o[:4])
    draft.box = slot
    draft.fallback = True
    if covered > 0:
        draft.flags.append("no_calm_zone")  # Touches a subject or sits on solid detail.
    return [draft]


def _layout_panel(
    panel: LetteringPanel,
    aspect: float,
    cfg: LayoutConfig,
    result: LetteringLayout,
    art_safety: ArtSafetyMap | None = None,
) -> None:
    p = Box(panel.rect.x * aspect, panel.rect.y, panel.rect.width * aspect, panel.rect.height)
    zones = sorted((_local_rect(z, p) for z in panel.reserved_zones), key=lambda z: (z.y, z.x))
    if art_safety:
        zones = [Box(z.x, z.y, z.width, min(z.height, ZONE_MAX_HEIGHT * p.height)) for z in zones]
    drafts = []
    for u in sorted(panel.utterances, key=lambda u: u.order):
        _, flags = select_font(u.text)
        if not u.text.strip():
            flags = ["empty_text"]
        if not zones:
            flags.append("missing_zone")
        if flags:
            for flag in flags:
                result.issues.append(
                    LetteringIssue(panel_id=panel.panel_id, utterance_id=u.utterance_id, flag=flag)
                )
            # Unsupported text is refused rather than emitting a misleading tofu bubble.
            continue
        drafts.append(_draft(u, p, zones, cfg))
    panel_faces = []
    named_faces: list[Subject] = []
    hard_boxes: list[Box] = []
    if art_safety:
        panel_rect = Box(panel.rect.x, panel.rect.y, panel.rect.width, panel.rect.height)
        panel_faces = [face for face in art_safety.face_boxes if panel_rect.overlaps(face)]
        named_faces = sorted(
            (
                s
                for s in art_safety.subjects
                if s.kind == "face" and s.character and panel_rect.overlaps(s.box)
            ),
            key=lambda s: -s.confidence,
        )
        hard_boxes = [
            Box(
                f.x * aspect - HARD_MARGIN,
                f.y - HARD_MARGIN,
                f.width * aspect + 2 * HARD_MARGIN,
                f.height + 2 * HARD_MARGIN,
            )
            for f in panel_faces
        ]

    def named_speaker_face(u: LetteringUtterance) -> Box | None:
        """The speaker's own face box, when the vision step named the character."""
        if u.speaker_anchor is not None or not u.speaker:
            return None
        return next((s.box for s in named_faces if same_character(u.speaker, s.character)), None)

    pinned_drafts = [
        d
        for d in drafts
        if d.utterance.placement and (d.utterance.placement.rect or d.utterance.placement.center)
    ]
    pinned_ids = {d.utterance.utterance_id for d in pinned_drafts}
    # A center override fixes the center, while letting long text shrink to the
    # documented minimum. Explicit rectangles remain exact and may be flagged.
    for index, draft in enumerate(pinned_drafts):
        placement = draft.utterance.placement
        if placement and placement.center and not placement.rect:
            for candidate in _draft_candidates(draft.utterance, p, zones, cfg):
                if _text_fits(candidate, aspect, cfg.padding):
                    pinned_drafts[index] = candidate
                    break
    pinned = [draft.box for draft in pinned_drafts]
    automatic = [d for d in drafts if d.utterance.utterance_id not in pinned_ids]

    def arrange(order: tuple[Draft, ...]):
        blockers = pinned[:]
        chosen: list[Draft] = []
        for original in order:
            u = original.utterance
            edge = u.caption_edge if u.kind in {"caption", "narration"} else None
            selected = None
            best_objective = math.inf
            requested = (
                cfg.font_size * (u.placement.size_scale or 1) if u.placement else cfg.font_size
            )
            for candidate_draft in _draft_candidates(u, p, zones, cfg):
                preferred = place(
                    candidate_draft.box, zones, blockers, cfg.gap, edge, (-math.inf, -math.inf)
                )
                if preferred is None:
                    if not art_safety:
                        continue
                    # Only a pull target: the art-aware search still scans the whole panel.
                    top = zones[0] if zones else p
                    preferred = Box(
                        top.x, top.y, candidate_draft.box.width, candidate_draft.box.height
                    )
                own_face = named_speaker_face(u) if u.kind in {"speech", "thought"} else None
                if own_face is not None:
                    # Sit above the speaker's own face so the tail points straight at it.
                    face_x, face_top = own_face.center[0] * aspect, own_face.y
                    preferred = Box(
                        min(
                            max(p.x, face_x - preferred.width / 2),
                            p.right - preferred.width,
                        ),
                        max(p.y, face_top - preferred.height - cfg.gap),
                        preferred.width,
                        preferred.height,
                    )
                elif u.kind in {"speech", "thought"} and panel_faces:
                    speaker_anchor = u.speaker_anchor
                    if speaker_anchor:
                        speaker_x = (
                            p.x + speaker_anchor[0] * p.width
                            if isinstance(speaker_anchor, tuple)
                            else _local_point(speaker_anchor, p)[0]
                        )
                    else:
                        speaker_face = min(
                            panel_faces,
                            key=lambda face: (
                                (face.center[0] * aspect - preferred.center[0]) ** 2
                                + (face.center[1] - preferred.center[1]) ** 2
                            ),
                        )
                        speaker_x = speaker_face.center[0] * aspect
                    zone = min(zones, key=lambda candidate: abs(candidate.y - preferred.y))
                    side_x = (
                        zone.right - candidate_draft.box.width
                        if speaker_x >= p.center[0]
                        else zone.x
                    )
                    side_candidate = Box(
                        side_x, preferred.y, candidate_draft.box.width, candidate_draft.box.height
                    )
                    if zone.contains(side_candidate) and not any(
                        side_candidate.overlaps(blocker, cfg.gap) for blocker in blockers
                    ):
                        preferred = side_candidate
                objective = 0.0
                if art_safety:
                    candidate, cost = place_in_calm_art(
                        candidate_draft.box,
                        zones,
                        blockers,
                        cfg.gap,
                        edge,
                        (-math.inf, -math.inf),
                        preferred,
                        art_safety,
                        aspect,
                        p,
                    )
                    if candidate is None:
                        continue
                    shortfall = max(0.0, requested - candidate_draft.size) / max(
                        requested - cfg.minimum_font_size, 1e-9
                    )
                    objective = cost + SIZE_COST * shortfall
                else:
                    candidate = preferred
                candidate_draft.box = candidate
                if not _text_fits(candidate_draft, aspect, cfg.padding):
                    continue
                if not art_safety:
                    selected = candidate_draft
                    break
                if objective < best_objective:
                    best_objective, selected = objective, candidate_draft
                    candidate_draft.cost = objective
            if selected is None:
                continue
            chosen.append(selected)
            blockers.append(selected.box)
        return chosen

    # Panels usually have one caption and one or two turns. Search all orders so
    # a tall speech bubble cannot be starved by a caption placed first.
    orders = list(itertools.permutations(automatic)) if len(automatic) <= 4 else [tuple(automatic)]
    arrangements = [arranged for order in orders if (arranged := arrange(order)) is not None]
    chosen: list[Draft] = []
    if arrangements:
        chosen = max(
            arrangements,
            # Rounded so that mirrored arrangements tie identically on every platform and
            # the first order in reading order wins, rather than float rounding noise.
            key=lambda arrangement: (
                len(arrangement),
                round(-sum(d.cost for d in arrangement), 6) if art_safety else 0,
                round(sum(d.size for d in arrangement), 9),
                round(-sum(d.box.width * d.box.height for d in arrangement), 9),
            ),
        )
    placed_ids = {d.utterance.utterance_id for d in chosen}
    # No line is left unplaced: shrink (done above), split a caption into two, then a margin
    # strip or the least-bad free slot, flagged for review only if it still touches a subject.
    for d in automatic:
        # Without any art pixels or boxes the zones stay hard and the old review flags apply.
        if d.utterance.utterance_id in placed_ids or not art_safety:
            continue
        extra = _fallback_drafts(
            d.utterance, p, cfg, aspect, [*pinned, *(c.box for c in chosen)], art_safety, hard_boxes
        )
        chosen.extend(extra)
        if extra:
            placed_ids.add(d.utterance.utterance_id)
    placed_drafts = pinned_drafts + chosen
    for d in automatic:
        if d.utterance.utterance_id not in placed_ids:
            flags = ["no_calm_zone"] if art_safety else ["overflow", "outside_reserved_zone"]
            if "below_minimum" in d.flags:
                flags.append("below_minimum")
            result.issues.extend(
                LetteringIssue(
                    panel_id=panel.panel_id, utterance_id=d.utterance.utterance_id, flag=flag
                )
                for flag in flags
            )
    drafts = placed_drafts
    rendered = [_lines(d, aspect) for d in drafts]
    all_text = [box for _, boxes in rendered for box in boxes]
    for i, (d, (lines, texts)) in enumerate(zip(drafts, rendered)):
        if not p.contains(d.box) or any(not p.contains(t) for t in texts):
            d.flags.append("out_of_panel")
        # With art (or vision boxes) a zone is only a hint, so even a manual placement may leave
        # it; without any art the zones stay hard.
        if not art_safety and not any(z.contains(d.box) for z in zones):
            d.flags.append("outside_reserved_zone")
        inner = Box(
            d.box.x + cfg.padding,
            d.box.y + cfg.padding,
            max(1e-9, d.box.width - 2 * cfg.padding),
            max(1e-9, d.box.height - 2 * cfg.padding),
        )
        if any(not inner.contains(t) for t in texts) or (
            d.utterance.kind in {"speech", "thought"}
            and any(not ellipse_contains(d.box, t) for t in texts)
        ):
            d.flags.append("overflow")
        if any(d.box.overlaps(other.box) for j, other in enumerate(drafts) if i != j):
            d.flags.append("collision")
        inferred_anchor = None
        own_face = (
            named_speaker_face(d.utterance) if d.utterance.kind in {"speech", "thought"} else None
        )
        if own_face is not None:
            inferred_anchor = _anchor_beside(own_face, d.box, aspect, p)
        elif (
            art_safety
            and d.utterance.kind in {"speech", "thought"}
            and not d.utterance.speaker_anchor
        ):
            centers = [face.center for face in panel_faces]
            if centers:
                center = min(
                    centers,
                    key=lambda point: (
                        (point[0] - d.box.center[0]) ** 2 + (point[1] - d.box.center[1]) ** 2
                    ),
                )
                inferred_anchor = (
                    (center[0] * aspect - p.x) / p.width,
                    (center[1] - p.y) / p.height,
                )
        # A tail to a known speaker's face reaches further so it is unmistakable who speaks.
        tail, circles = _tail(
            d, p, all_text, inferred_anchor, 0.06 if own_face is not None else 0.025
        )
        if any(not p.contains(c) for c in circles) or any(
            not (p.x <= x <= p.right and p.y <= y <= p.bottom) for x, y in tail
        ):
            d.flags.append("out_of_panel")
        review_flags = sorted(set(d.flags))
        flags = [flag for flag in review_flags if flag != "no_calm_zone"]
        bubble = LetteringBubble(
            panel_id=panel.panel_id,
            utterance_id=d.utterance.utterance_id,
            order=d.utterance.order,
            kind=d.utterance.kind,
            shape=LetteringShape(
                kind="ellipse" if d.utterance.kind in {"speech", "thought"} else "rounded_rect",
                bounds=_rect(d.box, aspect),
                radius=(
                    min(d.box.width, d.box.height) * 0.45
                    if d.utterance.kind == "speech"
                    else cfg.padding
                ),
            ),
            tail=[_point(x, y, aspect) for x, y in tail],
            thought_tail=[
                LetteringShape(kind="ellipse", bounds=_rect(c, aspect), radius=0) for c in circles
            ],
            lines=lines,
            flags=flags,
        )
        result.bubbles.append(bubble)
        result.issues.extend(
            LetteringIssue(panel_id=panel.panel_id, utterance_id=d.utterance.utterance_id, flag=f)
            for f in review_flags
        )


def source_digest(document: LetteringDocument) -> str:
    """Fingerprint the effective source, independent of transport ordering/defaults."""
    data = document.model_dump(mode="json", exclude_none=True)
    data["panels"].sort(key=lambda p: p["order"])
    for panel in data["panels"]:
        panel["utterances"].sort(key=lambda u: u["order"])
    return sha256(
        json.dumps(
            data, ensure_ascii=False, sort_keys=True, separators=(",", ":"), allow_nan=False
        ).encode()
    ).hexdigest()


def layout_page(
    document: LetteringDocument,
    config: LayoutConfig | None = None,
    art_image: bytes | None = None,
    subjects: Sequence[Subject] | None = None,
) -> LetteringLayout:
    """Compute a fresh draw list without mutating the input.

    Invalid identity/geometry raises ValueError; unrenderable or crowded lettering
    returns typed review issues. Automatic tails that cannot avoid text are omitted
    with an issue. Manual bubble geometry stays exact, including invalid placements.
    """
    config = config or LayoutConfig()
    _validate(document)
    result = LetteringLayout(
        version="lettering-layout-v1",
        page_id=document.page_id,
        source_digest=source_digest(document),
        page_aspect=document.page_aspect,
        minimum_font_size=config.minimum_font_size,
        bubbles=[],
        issues=[],
    )
    if document.art_mode == "baked_in":
        result.issues.append(LetteringIssue(panel_id="", utterance_id="", flag="baked_in_art"))
        return result
    art_safety = analyze_art(art_image) if art_image else None
    if subjects is not None:
        # Vision boxes replace the colour heuristics; without them the heuristics stay.
        art_safety = with_subjects(art_safety, subjects)
    for panel in sorted(document.panels, key=lambda p: p.order):
        _layout_panel(panel, document.page_aspect, config, result, art_safety)
    return result


def layout_bytes(result: LetteringLayout) -> bytes:
    """Canonical UTF-8 serialization for storage, golden comparisons and renderers."""
    return json.dumps(
        result.model_dump(mode="json"),
        ensure_ascii=False,
        sort_keys=True,
        separators=(",", ":"),
        allow_nan=False,
    ).encode("utf-8")
