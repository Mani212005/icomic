"""Face and subject-safe automatic lettering placement regressions."""

import json
from dataclasses import replace
from pathlib import Path

import numpy as np
import pytest
from PIL import Image, ImageDraw

from app.gen.models import LetteringDocument, LetteringOverride
from app.services.lettering import layout_page
from app.services.lettering.art_safety import analyze_art
from app.services.lettering.geometry import Box

FIXTURES = Path(__file__).parents[1] / "fixtures/lettering"
PROBE_STATE = json.loads((FIXTURES / "nia-brass-bird-lettering.state").read_text())
FACE_PANEL3 = (0.20, 0.655, 0.15, 0.105)  # Hand-marked Nia face rectangle in page coordinates.


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
    assert physical.x > 148 / 360 * (2 / 3)


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
