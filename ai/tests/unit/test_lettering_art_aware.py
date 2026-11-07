"""Face and subject-safe automatic lettering placement regressions."""

import json
from dataclasses import replace
from pathlib import Path

import numpy as np
import pytest
from PIL import Image, ImageDraw

from app.gen.models import LetteringDocument, LetteringOverride, LetteringUnitPoint
from app.services.lettering import layout_page
from app.services.lettering.art_safety import analyze_art
from app.services.lettering.geometry import Box

FIXTURES = Path(__file__).parents[1] / "fixtures/lettering"
PROBE_STATE = json.loads((FIXTURES / "nia-brass-bird-lettering.state").read_text())
FACE_PANEL3 = (0.20, 0.655, 0.15, 0.105)  # Hand-marked Nia face rectangle in page coordinates.
LIVE_2_TRUTH = json.loads((FIXTURES / "live-2/ground-truth.json").read_text())


def _doc(zone, *, panel_id="panel", text="Hello there!", kind="speech"):
    return LetteringDocument.model_validate(
        {
            "version": "lettering-v1",
            "page_id": "synthetic",
            "art_mode": "text_free",
            "page_aspect": 2 / 3,
            "panels": [
                {
                    "panel_id": panel_id,
                    "order": 0,
                    "rect": {"x": 0.02, "y": 0.02, "width": 0.96, "height": 0.96},
                    "reserved_zones": [zone],
                    "utterances": [
                        {
                            "utterance_id": "line",
                            "order": 0,
                            "kind": kind,
                            "text": text,
                            "speaker_anchor": {"x": 0.5, "y": 0.85},
                        }
                    ],
                }
            ],
        }
    )


def test_real_probe_refuses_panel_three_speech_without_hand_marked_face_clearance(monkeypatch):
    document = LetteringDocument.model_validate(PROBE_STATE["input"])
    image = (FIXTURES / "nia-brass-bird-raw.jpg").read_bytes()
    safety = analyze_art(image)
    monkeypatch.setattr(
        "app.services.lettering.analyze_art",
        lambda _: replace(safety, face_boxes=(*safety.face_boxes, Box(*FACE_PANEL3))),
    )

    result = layout_page(document, art_image=image)

    panel_id = document.panels[2].panel_id
    panel_speech = [b for b in result.bubbles if b.panel_id == panel_id and b.kind == "speech"]
    assert not panel_speech
    assert any(
        issue.panel_id == panel_id and issue.flag.root == "no_calm_zone" for issue in result.issues
    )


def test_real_probe_places_short_text_away_from_hand_marked_faces_and_birds(monkeypatch):
    document = LetteringDocument.model_validate(PROBE_STATE["input"])
    short_text = [
        "Storm.",
        "Bird!",
        "A boat.",
        "This way!",
        "Home!",
        "Fly!",
    ]
    for text, utterance in zip(short_text, [u for p in document.panels for u in p.utterances]):
        utterance.text = text
    document.panels[2].utterances[1].speaker_anchor = LetteringUnitPoint(x=0.32, y=0.39)
    document.panels[3].utterances[1].speaker_anchor = LetteringUnitPoint(x=0.42, y=0.39)
    image = (FIXTURES / "nia-brass-bird-raw.jpg").read_bytes()
    safety = analyze_art(image)

    # Hand-labeled normalized image rectangles: Nia faces in 1, 3, 4 and birds in 1-4.
    no_go = [
        Box(0.164, 0.133, 0.142, 0.101),
        Box(0.125, 0.635, 0.109, 0.097),
        Box(0.648, 0.635, 0.115, 0.103),
        Box(0.233, 0.557, 0.169, 0.115),
        Box(0.588, 0.078, 0.312, 0.194),
        Box(0.383, 0.788, 0.102, 0.040),
        Box(0.777, 0.724, 0.204, 0.151),
    ]
    monkeypatch.setattr(
        "app.services.lettering.analyze_art",
        lambda _: replace(
            safety,
            face_boxes=(
                *safety.face_boxes,
                *no_go,
            ),
        ),
    )

    result = layout_page(document, art_image=image)

    assert len([b for b in result.bubbles if not b.flags]) >= 3
    for bubble in result.bubbles:
        bounds = Box(**bubble.shape.bounds.model_dump())
        assert not any(bounds.overlaps(subject) for subject in no_go)

    assert all(not bubble.flags for bubble in result.bubbles if bubble.kind == "speech")


def test_live_2_gold_mask_covers_hand_marked_bird_bounds_with_margin():
    image = (FIXTURES / "nia-brass-bird-live-2.jpg").read_bytes()
    safety = analyze_art(image)
    assert (FIXTURES / "nia-brass-bird-live-2.jpg").stat().st_size < 300_000
    for bird in LIVE_2_TRUTH["birds"]:
        x, y, width, height = bird["rect"]
        bounds = Box(x * (2 / 3), y, width * (2 / 3), height)
        assert safety.gold_fraction(bounds, 2 / 3) >= 0.75, bird["panel"]
        assert safety.subject_fraction(bounds, 2 / 3) >= 0.75, bird["panel"]


def test_live_3_saved_art_layout_has_no_colliding_or_clipped_bubbles():
    state = json.loads((FIXTURES / "live-3/lettering-01.json").read_text())
    document = LetteringDocument.model_validate(state["input"])
    image = (FIXTURES / "live-3/nia-brass-bird.webp").read_bytes()

    result = layout_page(document, art_image=image)

    assert result.bubbles
    assert all(not bubble.flags for bubble in result.bubbles)
    assert not {
        "collision",
        "outside_reserved_zone",
        "overflow",
    } & {issue.flag.root for issue in result.issues}
    assert all(line.font_size >= 18 / 841.89 for bubble in result.bubbles for line in bubble.lines)
    no_go = [
        Box(x * (2 / 3), y, width * (2 / 3), height)
        for x, y, width, height in [
            (0.16, 0.13, 0.20, 0.17),  # upper-left face and hairline
            (0.63, 0.19, 0.09, 0.12),  # upper-right face
            (0.77, 0.15, 0.15, 0.12),  # upper-right brass bird
            (0.29, 0.28, 0.19, 0.15),  # hand and brass bird
            (0.27, 0.68, 0.17, 0.14),  # lower-left bird
            (0.03, 0.75, 0.13, 0.16),  # lower-left face and hand
            (0.73, 0.64, 0.17, 0.14),  # lower-right brass bird
            (0.43, 0.69, 0.12, 0.13),  # lower-right face
            (0.88, 0.70, 0.12, 0.15),  # lower-right hand
        ]
    ]
    assert all(
        not Box(
            bubble.shape.bounds.x,
            bubble.shape.bounds.y,
            bubble.shape.bounds.width,
            bubble.shape.bounds.height,
        ).overlaps(subject)
        for bubble in result.bubbles
        for subject in no_go
    )


def test_live_2_shrinks_captions_and_withholds_unsafe_speech():
    state = json.loads((FIXTURES / "live-2/lettering-01.json").read_text())
    document = LetteringDocument.model_validate(state["input"])
    image = (FIXTURES / "nia-brass-bird-live-2.jpg").read_bytes()
    result = layout_page(document, art_image=image)
    speech = [bubble for bubble in result.bubbles if bubble.kind == "speech"]
    assert speech == []
    assert len([issue for issue in result.issues if issue.flag.root == "no_calm_zone"]) >= 3
    assert all(line.font_size >= 18 / 841.89 for bubble in result.bubbles for line in bubble.lines)
    assert any(line.font_size < 35 / 841.89 for bubble in result.bubbles for line in bubble.lines)
    assert any(issue.flag.root == "no_calm_zone" for issue in result.issues)
    no_go = [
        Box(x * (2 / 3), y, width * (2 / 3), height)
        for group in (LIVE_2_TRUTH["faces"], LIVE_2_TRUTH["birds"])
        for item in group
        for x, y, width, height in [item["rect"]]
    ]
    assert all(
        not Box(
            bubble.shape.bounds.x,
            bubble.shape.bounds.y,
            bubble.shape.bounds.width,
            bubble.shape.bounds.height,
        ).overlaps(subject)
        for bubble in result.bubbles
        for subject in no_go
    )


def test_caption_anchors_outer_top_and_speech_uses_detected_speaker_side(monkeypatch):
    from io import BytesIO

    image = Image.new("RGB", (360, 540), "white")
    output = BytesIO()
    image.save(output, format="PNG")
    raw = output.getvalue()
    safety = replace(analyze_art(raw), face_boxes=(Box(0.68, 0.23, 0.08, 0.09),))
    monkeypatch.setattr("app.services.lettering.analyze_art", lambda _: safety)
    document = _doc(
        {"x": 0.04, "y": 0.04, "width": 0.92, "height": 0.8},
        text="At dawn.",
        kind="narration",
    )
    speech = (
        document.panels[0]
        .utterances[0]
        .model_copy(
            update={"utterance_id": "speech", "order": 1, "kind": "speech", "text": "This way!"}
        )
    )
    speech.speaker_anchor = LetteringUnitPoint(x=0.85, y=0.5)
    document.panels[0].utterances.append(speech)

    result = layout_page(document, art_image=raw)

    caption, bubble = result.bubbles
    assert caption.kind == "narration" and bubble.kind == "speech"
    assert caption.shape.bounds.y < bubble.shape.bounds.y
    assert bubble.shape.bounds.x + bubble.shape.bounds.width / 2 > 0.5
    assert not bubble.flags


def test_busy_subject_is_avoided_for_speech_and_narration():
    # The left half is detailed subject art; the right half is calm sky.
    rgb = np.full((240, 360, 3), (135, 185, 220), dtype=np.uint8)
    canvas = Image.fromarray(rgb)
    draw = ImageDraw.Draw(canvas)
    for x in range(24, 164, 12):
        draw.line((x, 25, x, 220), fill=(15, 20, 30), width=2)
    from io import BytesIO

    encoded = BytesIO()
    canvas.save(encoded, format="PNG")
    zone = {"x": 0.04, "y": 0.04, "width": 0.92, "height": 0.68}
    for kind in ("speech", "narration"):
        result = layout_page(_doc(zone, kind=kind), art_image=encoded.getvalue())
        bubble = result.bubbles[0]
        bounds = bubble.shape.bounds
        safety = analyze_art(encoded.getvalue())
        physical = Box(bounds.x * (2 / 3), bounds.y, bounds.width * (2 / 3), bounds.height)
        assert safety.busy_fraction(physical, 2 / 3) <= 0.20


def test_subject_silhouette_is_a_no_go_even_when_most_of_the_box_is_calm():
    image = Image.new("RGB", (360, 240), (135, 185, 220))
    draw = ImageDraw.Draw(image)
    draw.ellipse((52, 42, 128, 118), fill=(12, 34, 125))
    draw.rounded_rectangle((32, 102, 148, 238), radius=25, fill=(12, 34, 125))
    from io import BytesIO

    encoded = BytesIO()
    image.save(encoded, format="PNG")
    safety = analyze_art(encoded.getvalue())
    result = layout_page(
        _doc({"x": 0.04, "y": 0.04, "width": 0.92, "height": 0.68}),
        art_image=encoded.getvalue(),
    )
    bubble = result.bubbles[0]
    bounds = bubble.shape.bounds
    physical = Box(bounds.x * (2 / 3), bounds.y, bounds.width * (2 / 3), bounds.height)
    assert safety.subject_fraction(physical, 2 / 3) <= 0.08


def test_layout_is_deterministic_and_flags_when_no_calm_zone_exists():
    zone = {"x": 0.04, "y": 0.04, "width": 0.92, "height": 0.68}
    busy = Image.new("RGB", (240, 360))
    draw = ImageDraw.Draw(busy)
    for y in range(0, 360, 4):
        for x in range(0, 240, 4):
            color = (0, 0, 0) if (x // 4 + y // 4) % 2 else (255, 255, 255)
            draw.rectangle((x, y, x + 3, y + 3), fill=color)
    from io import BytesIO

    out = BytesIO()
    busy.save(out, format="PNG")
    document = _doc(zone)

    first = layout_page(document, art_image=out.getvalue())
    second = layout_page(document, art_image=out.getvalue())

    assert first.model_dump(mode="json") == second.model_dump(mode="json")
    assert "no_calm_zone" in {issue.flag.root for issue in first.issues}


def test_manual_override_keeps_exact_position_even_when_art_is_busy():
    from io import BytesIO

    art = Image.new("RGB", (240, 360))
    draw = ImageDraw.Draw(art)
    for y in range(0, 360, 4):
        for x in range(0, 240, 4):
            color = (0, 0, 0) if (x // 4 + y // 4) % 2 else (255, 255, 255)
            draw.rectangle((x, y, x + 3, y + 3), fill=color)
    output = BytesIO()
    art.save(output, format="PNG")
    document = _doc({"x": 0.04, "y": 0.04, "width": 0.92, "height": 0.68})
    document.panels[0].utterances[0].placement = LetteringOverride.model_validate(
        {"rect": {"x": 0.2, "y": 0.25, "width": 0.3, "height": 0.12}}
    )

    result = layout_page(document, art_image=output.getvalue())

    assert result.bubbles[0].shape.bounds.model_dump() == pytest.approx(
        {
            "x": 0.02 + 0.2 * 0.96,
            "y": 0.02 + 0.25 * 0.96,
            "width": 0.3 * 0.96,
            "height": 0.12 * 0.96,
        }
    )
