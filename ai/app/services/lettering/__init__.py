"""Deterministic, opt-in vector lettering. Never accepts or modifies page pixels."""

import json
import math
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
from .art_safety import ArtSafetyMap, analyze_art, place_in_calm_art
from .geometry import Box, ellipse_contains, place, polygon_hits_box
from .typography import Run, select_font, shape, wrap


@dataclass(frozen=True)
class LayoutConfig:
    # 35pt / 841.89pt A4 height keeps glyphs near 55px on 1280x1920 art.
    # Never shrink below the print-readable minimum.
    minimum_font_size: float = 35 / 841.89
    font_size: float = 35 / 841.89
    padding: float = 0.006
    gap: float = 0.012

    def __post_init__(self):
        values = (self.minimum_font_size, self.font_size, self.padding, self.gap)
        if (
            not all(math.isfinite(v) and v > 0 for v in values)
            or self.minimum_font_size < 35 / 841.89
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


def _draft(u: LetteringUtterance, panel: Box, zones: list[Box], cfg: LayoutConfig) -> Draft:
    font_id, flags = select_font(u.text)
    requested = cfg.font_size * (u.placement.size_scale or 1) if u.placement else cfg.font_size
    size = max(cfg.minimum_font_size, requested)
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
        width = min(widest, panel.width * 0.94)
    elif u.kind == "speech":
        width = min(widest, panel.width * 0.45)
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


def _tail(
    d: Draft,
    panel: Box,
    texts: list[Box],
    inferred_anchor: tuple[float, float] | None = None,
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
    if distance > 0.025 and not manual:
        ax, ay = ex + (ax - ex) * 0.025 / distance, ey + (ay - ey) * 0.025 / distance
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


def _layout_panel(
    panel: LetteringPanel,
    aspect: float,
    cfg: LayoutConfig,
    result: LetteringLayout,
    art_safety: ArtSafetyMap | None = None,
) -> None:
    p = Box(panel.rect.x * aspect, panel.rect.y, panel.rect.width * aspect, panel.rect.height)
    zones = sorted((_local_rect(z, p) for z in panel.reserved_zones), key=lambda z: (z.y, z.x))
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
    pinned = [
        d.box
        for d in drafts
        if d.utterance.placement and (d.utterance.placement.rect or d.utterance.placement.center)
    ]
    occupied = pinned[:]
    floor = (-math.inf, -math.inf)
    skipped_utterances = set()
    panel_faces = []
    if art_safety:
        panel_rect = Box(panel.rect.x, panel.rect.y, panel.rect.width, panel.rect.height)
        panel_faces = [face for face in art_safety.face_boxes if panel_rect.overlaps(face)]
    for d in drafts:
        u = d.utterance
        if u.placement and (u.placement.rect or u.placement.center):
            continue
        edge = u.caption_edge if u.kind in {"caption", "narration"} else None
        preferred = place(d.box, zones, occupied, cfg.gap, edge, floor)
        if preferred is not None and u.kind in {"speech", "thought"} and panel_faces:
            speaker_anchor = (
                u.placement.tail_anchor
                if u.placement and u.placement.tail_anchor
                else u.speaker_anchor
            )
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
            on_left = speaker_x <= p.center[0]
            side_x = zone.x if on_left else zone.right - d.box.width
            preferred = Box(side_x, preferred.y, d.box.width, d.box.height)
        if art_safety and preferred is not None:
            candidate, no_calm_zone = place_in_calm_art(
                d.box,
                [*zones, p],
                occupied,
                cfg.gap,
                edge,
                floor,
                preferred,
                art_safety,
                aspect,
            )
            if no_calm_zone:
                d.flags.append("no_calm_zone")
        else:
            candidate = preferred
        if candidate is None:
            if art_safety:
                skipped_utterances.add(u.utterance_id)
                result.issues.append(
                    LetteringIssue(
                        panel_id=panel.panel_id,
                        utterance_id=u.utterance_id,
                        flag="no_calm_zone",
                    )
                )
                continue
            zone = zones[0]
            y = zone.bottom - d.box.height if edge == "bottom" else zone.y
            candidate = Box(zone.x, y, d.box.width, d.box.height)
            d.flags.append("overflow")
        d.box = candidate
        occupied.append(candidate)
        floor = max(floor, (candidate.y, candidate.x))
    if skipped_utterances:
        drafts = [d for d in drafts if d.utterance.utterance_id not in skipped_utterances]
    rendered = [_lines(d, aspect) for d in drafts]
    all_text = [box for _, boxes in rendered for box in boxes]
    for i, (d, (lines, texts)) in enumerate(zip(drafts, rendered)):
        if not p.contains(d.box) or any(not p.contains(t) for t in texts):
            d.flags.append("out_of_panel")
        if not any(z.contains(d.box) for z in zones) and (
            not art_safety
            or (
                d.utterance.placement
                and (d.utterance.placement.rect or d.utterance.placement.center)
            )
        ):
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
        if (
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
        tail, circles = _tail(d, p, all_text, inferred_anchor)
        if any(not p.contains(c) for c in circles) or any(
            not (p.x <= x <= p.right and p.y <= y <= p.bottom) for x, y in tail
        ):
            d.flags.append("out_of_panel")
        flags = sorted(set(d.flags))
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
            for f in flags
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
