"""Hermetic golden and seeded property coverage for standalone vector lettering."""

import hashlib
import math
import random
from copy import deepcopy
from pathlib import Path

import pytest
from PIL import Image
from pydantic import ValidationError

from app.gen.models import LetteringDocument, LetteringLayout, LetteringUnitPoint
from app.services.lettering import LayoutConfig, layout_bytes, layout_page
from app.services.lettering.geometry import Box, ellipse_contains, polygon_hits_box
from app.services.lettering.typography import FONT_DIRECTORY, FONT_FILES, shape, wrap

FIXTURES = Path(__file__).parents[1] / "fixtures" / "lettering"


def document(text="We found it!", **utterance):
    return LetteringDocument.model_validate(
        {
            "version": "lettering-v1",
            "page_id": "page-1",
            "art_mode": "text_free",
            "page_aspect": 210 / 297,
            "panels": [
                {
                    "panel_id": "panel-1",
                    "order": 0,
                    "rect": {"x": 0.05, "y": 0.05, "width": 0.9, "height": 0.9},
                    "reserved_zones": [{"x": 0.03, "y": 0.03, "width": 0.94, "height": 0.6}],
                    "utterances": [
                        {
                            "utterance_id": "u-1",
                            "order": 0,
                            "kind": "speech",
                            "text": text,
                            **utterance,
                        }
                    ],
                }
            ],
        }
    )


def flags(result):
    return {issue.flag.root for issue in result.issues}


def box(rect):
    return Box(rect.x, rect.y, rect.width, rect.height)


def utterance_copy(doc, number, text="Yes!"):
    u = doc.panels[0].utterances[0].model_copy(deep=True)
    u.utterance_id, u.order, u.text, u.speaker = f"u-{number}", number - 1, text, f"person-{number}"
    return u


@pytest.mark.parametrize("name", sorted(p.stem for p in FIXTURES.glob("*.input.json")))
def test_golden(name):
    # stem retains .input, so pair paths by the documented naming convention.
    source = FIXTURES / f"{name}.json"
    expected = FIXTURES / f"{name.removesuffix('.input')}.golden.json"
    doc = LetteringDocument.model_validate_json(source.read_bytes())
    before = doc.model_dump_json()
    actual = layout_page(doc)
    assert layout_bytes(actual) == expected.read_bytes().rstrip(b"\n")
    assert layout_bytes(LetteringLayout.model_validate_json(layout_bytes(actual))) == layout_bytes(
        actual
    )
    assert doc.model_dump_json() == before


def test_wrap_uses_glyph_width_and_never_breaks_combining_or_conjunct_clusters():
    for text, font_id in [
        ("Cafe\u0301 déjà vu", "comic-neue-v1"),
        ("नमस्ते दुनिया", "noto-devanagari-v1"),
    ]:
        lines = wrap(text, font_id, 0.02, 0.045)
        assert " ".join(lines) == text
        assert all(not line.startswith("\u0301") for line in lines)
    assert shape("e\u0301", "comic-neue-v1", 0.02).width == pytest.approx(
        shape("é", "comic-neue-v1", 0.02).width
    )
    run = shape("क्षि", "noto-devanagari-v1", 0.02)
    assert all(g[0] != 0 for g in run.glyphs)
    assert len(run.glyphs) != len("क्षि")  # conjunct/mark shaping, not codepoint metrics


def test_empty_and_unsupported_text_is_refused_with_typed_issues():
    for text, expected in [
        ("", "empty_text"),
        ("  \n ", "empty_text"),
        ("Hello 😀", "unsupported_glyph"),
        ("שלום", "unsupported_direction"),
        ("Hello\x00", "unsupported_glyph"),
    ]:
        result = layout_page(document(text))
        assert expected in flags(result)
        assert result.bubbles == []


def test_minimum_print_size_and_large_text_is_flagged_instead_of_shrunk():
    doc = document("A very long sentence " * 400, placement={"size_scale": 0.01})
    result = layout_page(doc)
    assert {"overflow", "below_minimum"} <= flags(result)
    assert all(line.font_size >= 14 / 841.89 for b in result.bubbles for line in b.lines)
    with pytest.raises(ValueError):
        LayoutConfig(minimum_font_size=0.001)


def test_missing_zones_and_baked_in_art_never_get_a_lettering_layer():
    doc = document()
    doc.panels[0].reserved_zones = []
    result = layout_page(doc)
    assert flags(result) == {"missing_zone"}
    assert result.bubbles == []
    doc.art_mode = "baked_in"
    result = layout_page(doc)
    assert flags(result) == {"baked_in_art"}
    assert result.bubbles == []


def test_manual_center_rect_anchor_and_size_are_exact_and_revalidated():
    result = layout_page(
        document(
            placement={
                "center": {"x": 0.4, "y": 0.3},
                "size_scale": 1.4,
                "tail_anchor": {"x": 0.8, "y": 0.8},
            }
        )
    )
    b = result.bubbles[0]
    assert box(b.shape.bounds).center == pytest.approx((0.05 + 0.9 * 0.4, 0.05 + 0.9 * 0.3))
    assert (b.tail[4].x, b.tail[4].y) == pytest.approx((0.05 + 0.9 * 0.8, 0.05 + 0.9 * 0.8))
    assert b.lines[0].font_size == pytest.approx(LayoutConfig().font_size * 1.4)
    assert not flags(result)
    placement = {
        "rect": {"x": -0.1, "y": 0.9, "width": 0.05, "height": 0.05},
        "center": {"x": 0.2, "y": 0.2},
    }
    result = layout_page(document(placement=placement))
    b = result.bubbles[0]
    assert b.shape.bounds.model_dump() == pytest.approx(
        {"x": -0.04, "y": 0.86, "width": 0.045, "height": 0.045}
    )
    assert {"out_of_panel", "outside_reserved_zone", "overflow"} <= flags(result)


def test_manual_collisions_are_preserved_and_automatic_bubbles_avoid_pinned_overrides():
    doc = document(placement={"rect": {"x": 0.05, "y": 0.05, "width": 0.3, "height": 0.15}})
    doc.panels[0].utterances.append(utterance_copy(doc, 2))
    result = layout_page(doc)
    assert "collision" in flags(result)
    assert result.bubbles[0].shape.bounds == result.bubbles[1].shape.bounds
    doc.panels[0].utterances[0].placement = None
    result = layout_page(doc)
    assert "collision" not in flags(result)
    assert not box(result.bubbles[0].shape.bounds).overlaps(box(result.bubbles[1].shape.bounds))


def test_impossible_placement_and_blocked_tail_are_review_issues():
    doc = document("Hi")
    doc.panels[0].reserved_zones[0].width = 0.02
    doc.panels[0].reserved_zones[0].height = 0.02
    result = layout_page(doc)
    assert {"overflow", "outside_reserved_zone"} <= flags(result)
    # The anchor is inside the other speaker's text: no safe tail can be emitted.
    doc = document(
        "First",
        placement={
            "rect": {"x": 0.05, "y": 0.05, "width": 0.3, "height": 0.15},
            "tail_anchor": {"x": 0.6, "y": 0.3},
        },
    )
    doc.panels[0].utterances.append(utterance_copy(doc, 2, "Second"))
    doc.panels[0].utterances[1].placement.rect.x = 0.45
    doc.panels[0].utterances[1].placement.rect.y = 0.225
    result = layout_page(doc)
    assert "tail_crosses_text" in flags(result)
    assert result.bubbles[0].tail == []


def test_captions_anchor_to_top_and_bottom_of_reserved_zone():
    for kind in ("caption", "narration"):
        for edge in ("top", "bottom"):
            doc = document("Later that day.", kind=kind, caption_edge=edge)
            result = layout_page(doc)
            b = result.bubbles[0]
            assert not flags(result)
            assert b.shape.kind == "rounded_rect"
            assert not b.tail and not b.thought_tail
            if edge == "top":
                assert b.shape.bounds.y == pytest.approx(0.05 + 0.9 * 0.03)
            else:
                assert box(b.shape.bounds).bottom == pytest.approx(0.05 + 0.9 * 0.63)


def test_ids_order_and_finite_geometry_are_validated():
    doc = document()
    duplicate = utterance_copy(doc, 1)
    doc.panels[0].utterances.append(duplicate)
    with pytest.raises(ValueError, match="unique"):
        layout_page(doc)
    doc = document()
    doc.panels[0].rect.width = 1
    with pytest.raises(ValueError, match="beyond"):
        layout_page(doc)
    doc = document()
    doc.panels[0].reserved_zones[0].x = 0.8
    with pytest.raises(ValueError, match="beyond"):
        layout_page(doc)
    doc = document(placement={"center": {"x": float("nan"), "y": 0.2}})
    with pytest.raises(ValueError):
        layout_page(doc)
    data = document().model_dump(mode="json")
    data["image_url"] = "not-an-input"
    with pytest.raises(ValidationError):
        LetteringDocument.model_validate(data)


def test_seeded_properties_bounds_collision_reading_order_and_determinism():
    rng = random.Random(9381)
    for _ in range(80):
        doc = document()
        doc.page_aspect = rng.choice([210 / 297, 2 / 3, 1.0])
        p = doc.panels[0]
        p.rect.x, p.rect.y = rng.uniform(0.01, 0.1), rng.uniform(0.01, 0.1)
        p.rect.width, p.rect.height = rng.uniform(0.6, 0.85), rng.uniform(0.6, 0.85)
        p.reserved_zones[0].height = 0.85
        count = rng.randint(1, 5)
        p.utterances = [
            utterance_copy(doc, n, rng.choice(["Go!", "We can do this.", "Café!"]))
            for n in range(1, count + 1)
        ]
        for u in p.utterances:
            u.speaker_anchor = LetteringUnitPoint(
                x=0.8, y=0.95
            )  # reconstruct below for model validation
        doc = LetteringDocument.model_validate(doc.model_dump(mode="json"))
        result = layout_page(doc)
        assert not flags(result), flags(result)
        assert layout_bytes(result) == layout_bytes(layout_page(doc))
        positions = []
        text_boxes = [box(line.bounds) for b in result.bubbles for line in b.lines]
        for i, bubble in enumerate(result.bubbles):
            bounds = box(bubble.shape.bounds)
            positions.append((bounds.y, bounds.x))
            assert box(p.rect).contains(bounds)
            assert all(bounds.contains(box(line.bounds)) for line in bubble.lines)
            assert all(box(p.rect).contains(box(c.bounds)) for c in bubble.thought_tail)
            assert not any(
                bounds.overlaps(box(other.shape.bounds)) for other in result.bubbles[i + 1 :]
            )
            assert not any(
                polygon_hits_box([(point.x, point.y) for point in bubble.tail], t)
                for t in text_boxes
            )
            assert all(
                p.rect.x <= point.x <= p.rect.x + p.rect.width
                and p.rect.y <= point.y <= p.rect.y + p.rect.height
                for point in bubble.tail
            )
        assert positions == sorted(positions)
        # Input array reordering cannot change stable explicit order.
        reversed_doc = doc.model_copy(deep=True)
        reversed_doc.panels[0].utterances.reverse()
        assert layout_bytes(result) == layout_bytes(layout_page(reversed_doc))


def test_multiple_panels_use_page_rectangles_and_global_ids():
    doc = document()
    p = doc.panels[0]
    p.rect.height = 0.4
    second = p.model_copy(deep=True)
    second.panel_id, second.order, second.rect.y = "panel-2", 1, 0.55
    second.utterances[0].utterance_id = "u-2"
    doc.panels = [second, p]
    result = layout_page(doc)
    assert [b.panel_id for b in result.bubbles] == ["panel-1", "panel-2"]
    assert not flags(result)
    second.utterances[0].utterance_id = "u-1"
    with pytest.raises(ValueError, match="unique"):
        layout_page(doc)


def test_source_page_hash_unchanged_and_engine_never_opens_or_writes_pixels(tmp_path, monkeypatch):
    source = tmp_path / "source.png"
    Image.new("RGB", (40, 60), "skyblue").save(source)
    before = hashlib.sha256(source.read_bytes()).hexdigest()

    def forbidden(*args, **kwargs):
        pytest.fail("Lettering engine attempted pixel I/O")

    monkeypatch.setattr(Image, "open", forbidden)
    monkeypatch.setattr(Image.Image, "save", forbidden)
    input_doc = document()
    untouched = deepcopy(input_doc.model_dump(mode="json"))
    layout_page(input_doc)
    assert input_doc.model_dump(mode="json") == untouched
    assert hashlib.sha256(source.read_bytes()).hexdigest() == before


def test_bundled_fonts_and_licenses_are_present_and_versioned():
    for font_id, filename in FONT_FILES.items():
        assert (FONT_DIRECTORY / filename).stat().st_size < 200_000
        assert "v1" in font_id
        license_path = FONT_DIRECTORY / (filename.split("-Regular")[0] + "-OFL.txt")
        assert "SIL OPEN FONT LICENSE Version 1.1" in license_path.read_text()


def test_thought_ellipse_contains_text_and_tail_circles_avoid_text():
    doc = document(
        "Is this really the answer?", kind="thought", speaker_anchor={"x": 0.8, "y": 0.8}
    )
    result = layout_page(doc)
    assert not flags(result)
    bubble = result.bubbles[0]
    assert bubble.shape.kind == "ellipse"
    assert len(bubble.thought_tail) == 3
    assert all(
        ellipse_contains(box(bubble.shape.bounds), box(line.bounds)) for line in bubble.lines
    )
    assert not any(
        box(c.bounds).overlaps(box(line.bounds))
        for c in bubble.thought_tail
        for line in bubble.lines
    )
    assert not bubble.tail
    last = box(bubble.thought_tail[-1].bounds)
    cx, cy = last.center
    # Automatic thought tails point toward the speaker with a short trail;
    # they no longer extend all the way across the panel to the speaker.
    body = bubble.shape.bounds
    assert cy > body.y + body.height
    assert cy - (body.y + body.height) <= 0.035
    assert cx < 0.05 + 0.9 * 0.8


def test_multiple_reserved_zones_and_deterministic_push_down():
    doc = document("Follow me.")
    doc.panels[0].reserved_zones[0].width = 0.16
    doc.panels[0].reserved_zones[0].height = 0.075
    second_zone = doc.panels[0].reserved_zones[0].model_copy(update={"x": 0.55, "height": 0.5})
    doc.panels[0].reserved_zones.append(second_zone)
    doc.panels[0].utterances.extend([utterance_copy(doc, 2), utterance_copy(doc, 3)])
    doc.panels[0].utterances[-1].speaker_anchor = LetteringUnitPoint(x=0.95, y=0.8)
    result = layout_page(doc)
    assert not flags(result)
    a, b, c = [box(bubble.shape.bounds) for bubble in result.bubbles]
    assert a.x < b.x
    assert c.y > b.y
    assert not a.overlaps(b) and not b.overlaps(c)


def test_comic_proportions_centered_speech_short_curved_tail_and_compact_caption():
    doc = document("Follow the light!", speaker_anchor={"x": 0.6, "y": 0.9})
    result = layout_page(doc)
    bubble = result.bubbles[0]
    assert not flags(result)
    assert bubble.shape.kind == "ellipse"
    assert bubble.shape.bounds.x + bubble.shape.bounds.width / 2 == pytest.approx(0.5)
    assert result.stroke_width * 1920 == pytest.approx(2.496)
    assert all(line.font_size * 841.89 >= 14 for line in bubble.lines)
    # Sampled sides curve toward a short point, instead of the old triangle needle.
    assert len(bubble.tail) == 9
    base = bubble.tail[0]
    tip = bubble.tail[4]
    assert math.hypot((tip.x - base.x) * doc.page_aspect, tip.y - base.y) < 0.045
    assert tip.y < 0.05 + 0.9 * 0.9
    caption = layout_page(document("Before dawn.", kind="narration")).bubbles[0]
    assert len(caption.lines) == 1
    assert caption.shape.bounds.width <= doc.panels[0].rect.width * 0.6
    assert caption.shape.bounds.width < bubble.shape.bounds.width
