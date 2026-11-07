"""Face and subject-safe automatic lettering placement regressions."""

import json
from dataclasses import replace
from pathlib import Path

import numpy as np
import pytest
from PIL import Image, ImageDraw

from app.gen.models import LetteringDocument, LetteringOverride, LetteringUnitPoint
from app.services.lettering import art_safety, layout_page
from app.services.lettering.art_safety import Subject, analyze_art, place_in_calm_art, slot_cost
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


def test_panel_three_speech_never_covers_the_hand_marked_face(monkeypatch):
    document = LetteringDocument.model_validate(PROBE_STATE["input"])
    image = (FIXTURES / "nia-brass-bird-raw.jpg").read_bytes()
    safety = analyze_art(image)
    monkeypatch.setattr(
        "app.services.lettering.analyze_art",
        lambda _: replace(safety, face_boxes=(*safety.face_boxes, Box(*FACE_PANEL3))),
    )

    result = layout_page(document, art_image=image)

    panel_id = document.panels[2].panel_id
    face = Box(*FACE_PANEL3)
    for bubble in result.bubbles:
        if bubble.panel_id == panel_id:
            assert not Box(**bubble.shape.bounds.model_dump()).overlaps(face)
    # A line that cannot be placed is withheld with a review issue, never drawn over the face.
    placed = {bubble.utterance_id for bubble in result.bubbles}
    for utterance in document.panels[2].utterances:
        if utterance.utterance_id not in placed:
            assert any(
                issue.utterance_id == utterance.utterance_id and issue.flag.root == "no_calm_zone"
                for issue in result.issues
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


LIVE_3_NO_GO = [  # Hand-marked page-normalized faces, birds and hands in the live-3 art.
    Box(*rect)
    for rect in [
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
LIVE_2_NO_GO = [
    Box(*item["rect"]) for group in ("faces", "birds", "hands") for item in LIVE_2_TRUTH[group]
]
SAVED_PAGES = {
    "live-3": ("live-3/lettering-01.json", "live-3/nia-brass-bird.webp", LIVE_3_NO_GO),
    "live-2": ("live-2/lettering-01.json", "nia-brass-bird-live-2.jpg", LIVE_2_NO_GO),
}


def _saved_page(name):
    state_file, art_file, no_go = SAVED_PAGES[name]
    document = LetteringDocument.model_validate(
        json.loads((FIXTURES / state_file).read_text())["input"]
    )
    return document, (FIXTURES / art_file).read_bytes(), no_go


def _bounds(bubble):
    return Box(**bubble.shape.bounds.model_dump())


def _assert_complete_clean_layout(result, document):
    lines = [u for panel in document.panels for u in panel.utterances]
    speech = {u.utterance_id for u in lines if u.kind == "speech"}
    assert len(result.bubbles) >= 0.8 * len(lines)
    assert speech <= {bubble.utterance_id for bubble in result.bubbles}
    assert all(not bubble.flags for bubble in result.bubbles)
    assert not result.issues  # No review note, no withheld line.
    assert all(line.font_size >= 18 / 841.89 for bubble in result.bubbles for line in bubble.lines)


@pytest.mark.parametrize("name", ["live-3", "live-2"])
def test_saved_page_places_every_line_clear_of_hand_marked_subjects(name, monkeypatch):
    document, image, no_go = _saved_page(name)
    safety = analyze_art(image)
    # Hand-marked faces, birds and hands are hard no-go boxes, as in the other probes.
    monkeypatch.setattr(
        "app.services.lettering.analyze_art",
        lambda _: replace(safety, face_boxes=(*safety.face_boxes, *no_go)),
    )

    result = layout_page(document, art_image=image)

    _assert_complete_clean_layout(result, document)
    for bubble in result.bubbles:
        assert not any(_bounds(bubble).overlaps(subject) for subject in no_go)


@pytest.mark.parametrize("name", ["live-3", "live-2"])
def test_saved_page_unaided_layout_is_complete_and_nearly_clear(name):
    document, image, no_go = _saved_page(name)

    result = layout_page(document, art_image=image)

    _assert_complete_clean_layout(result, document)
    # Detection is heuristic and the hand-drawn rectangles are loose: a bubble may only graze
    # a marked rectangle's edge (at most a quarter of its own area), never cover the subject.
    for bubble in result.bubbles:
        box = _bounds(bubble)
        for subject in no_go:
            ox = min(box.right, subject.right) - max(box.x, subject.x)
            oy = min(box.bottom, subject.bottom) - max(box.y, subject.y)
            if ox > 0 and oy > 0:
                assert ox * oy <= 0.25 * box.width * box.height, (name, bubble.utterance_id)


def test_live_2_ground_truth_marks_hands_in_every_panel():
    assert {hand["panel"] for hand in LIVE_2_TRUTH["hands"]} == {1, 2, 3, 4}


def _calm_map():
    image = Image.new("RGB", (360, 540), (135, 185, 220))
    from io import BytesIO

    out = BytesIO()
    image.save(out, format="PNG")
    return analyze_art(out.getvalue())


PANEL = Box(0.02 * 2 / 3, 0.02, 0.96 * 2 / 3, 0.96)
SLOT = Box(0.06, 0.05, 0.18, 0.06)


def test_slot_cost_rises_with_subject_detail_and_distance_from_the_top():
    calm = _calm_map()
    low_confidence = replace(calm, subjects=(Subject(Box(0.0, 0.0, 1.0, 1.0), "face", 0.05),))
    high_confidence = replace(calm, subjects=(Subject(Box(0.0, 0.0, 1.0, 1.0), "face", 0.95),))
    base = slot_cost(SLOT, PANEL, calm, 2 / 3)
    # A low-confidence "face" (a golden sky) is nearly free; a confident one is not.
    assert slot_cost(SLOT, PANEL, low_confidence, 2 / 3) - base < 0.3 * art_safety.COST_FACE
    assert slot_cost(SLOT, PANEL, high_confidence, 2 / 3) - base > 0.8 * art_safety.COST_FACE
    lower = Box(SLOT.x, SLOT.y + 0.4, SLOT.width, SLOT.height)
    assert slot_cost(lower, PANEL, calm, 2 / 3) > base


def test_confident_face_is_a_hard_no_go_while_a_weak_one_is_only_a_cost():
    calm = _calm_map()
    zone = Box(PANEL.x, PANEL.y, PANEL.width, 0.2)
    size = Box(0, 0, 0.18, 0.06)
    face = Box(0.0, 0.0, 1.0, 0.23)  # Covers the whole zone in page coordinates.
    weak = replace(calm, subjects=(Subject(face, "face", 0.05),))
    strong = replace(calm, face_boxes=(face,))

    assert place_in_calm_art(size, [zone], [], 0.012, None, (-9, -9), zone, weak, 2 / 3, PANEL)[0]
    box, cost = place_in_calm_art(
        size, [zone], [], 0.012, None, (-9, -9), zone, strong, 2 / 3, PANEL
    )
    # The zone is fully covered by a confident face, so the search leaves it for the panel.
    assert box is not None and not face.overlaps(box) and cost < float("inf")


def test_panel_margin_is_used_when_the_reserved_zone_is_full():
    calm = _calm_map()
    zone = Box(PANEL.x, PANEL.y, 0.1, 0.02)  # Too small for the slot.
    size = Box(0, 0, 0.18, 0.06)

    box, _ = place_in_calm_art(size, [zone], [], 0.012, None, (-9, -9), zone, calm, 2 / 3, PANEL)

    assert box is not None and PANEL.contains(box) and not zone.contains(box)


def test_nothing_is_placed_when_every_slot_is_confidently_covered():
    calm = _calm_map()
    everything = Box(0.0, 0.0, 1.0, 1.0)
    covered = replace(calm, face_boxes=(everything,))

    box, cost = place_in_calm_art(
        Box(0, 0, 0.18, 0.06), [PANEL], [], 0.012, None, (-9, -9), PANEL, covered, 2 / 3, PANEL
    )

    assert box is None and cost == float("inf")


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
    assert caption.shape.bounds.y < 0.1  # Anchored to the outer top of the panel.
    assert not Box(**caption.shape.bounds.model_dump()).overlaps(
        Box(**bubble.shape.bounds.model_dump())
    )
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
