"""Offline lettering corpus: every saved painted page plus synthetic stress variants.

The pages are real text-free model art (downscaled) with hand-marked face, hand and bird
boxes standing in for the vision step. Each must letter completely: every line placed, no
bubble or caption over a marked subject, no review flag, a clean PDF export decision and tails
that point at the speaker's own face.
"""

import json
import math
from io import BytesIO
from pathlib import Path

import numpy as np
import pytest
from PIL import Image

from app.gen.models import LetteringDocument, LetteringUtterance, PageLetteringState
from app.services.lettering import layout_page, same_character
from app.services.lettering.art_safety import Subject
from app.services.lettering.geometry import Box
from app.services.lettering.pdf import eligibility

CORPUS = Path(__file__).parents[1] / "fixtures/lettering/corpus"
PAGES = sorted(path.stem for path in CORPUS.glob("*.jpg"))
VISION_PAGES = sorted(
    path.name.removesuffix(".vision.json") for path in CORPUS.glob("*.vision.json")
)
BOX_SETS = [(name, "hand") for name in PAGES] + [(name, "vision") for name in VISION_PAGES]
ASPECT = 2 / 3
MIN_FONT = 18 / 841.89


def _subjects(items):
    return [
        Subject(Box(*item["rect"]), item["kind"], item["confidence"], item["character"])
        for item in items
    ]


def _load(name, boxes="hand"):
    """The page, its art and the boxes that stand in for the vision step.

    `hand` boxes were marked by eye on the art; `vision` boxes are the recorded output of one
    real vision call. Either way the result is also checked against the hand-marked boxes.
    """
    data = json.loads((CORPUS / f"{name}.json").read_text())
    document = LetteringDocument.model_validate(data["document"])
    marked = _subjects(data["subjects"])
    if boxes == "vision":
        fed = _subjects(json.loads((CORPUS / f"{name}.vision.json").read_text())["subjects"])
        return document, (CORPUS / f"{name}.jpg").read_bytes(), fed, marked
    return document, (CORPUS / f"{name}.jpg").read_bytes(), marked, marked


def _bounds(bubble):
    return Box(**bubble.shape.bounds.model_dump())


def _panel_of(document, panel_id):
    return next(p for p in document.panels if p.panel_id == panel_id)


def _speaker_faces(document, subjects, panel):
    rect = Box(panel.rect.x, panel.rect.y, panel.rect.width, panel.rect.height)
    return [s for s in subjects if s.kind == "face" and rect.overlaps(s.box)]


def _assert_clean_and_complete(result, document, subjects, cross_checked=()):
    lines = [u for panel in document.panels for u in panel.utterances]
    # Every utterance is drawn, as itself or as the parts of a split caption.
    placed = {}
    for bubble in result.bubbles:
        placed.setdefault(bubble.utterance_id.split("~")[0], []).append(bubble)
    assert not result.issues, result.issues
    for line in lines:
        bubbles = placed.get(line.utterance_id)
        assert bubbles, f"unplaced: {line.utterance_id}"
        drawn = " ".join(
            part.text
            for bubble in sorted(bubbles, key=lambda b: b.utterance_id)
            for part in bubble.lines
        )
        assert drawn.split() == line.text.split(), "text must never be trimmed or reordered"
    # Faces, hands and birds are hard exclusions; key objects only add placement cost.
    boxes = [b.box for b in subjects if b.kind in ("face", "hand", "bird")]
    for bubble in result.bubbles:
        assert not bubble.flags, (bubble.utterance_id, bubble.flags)
        assert all(line.font_size >= MIN_FONT - 1e-9 for line in bubble.lines)
        physical = _bounds(bubble)
        page_box = Box(physical.x, physical.y, physical.width, physical.height)
        for subject in boxes:
            assert not page_box.overlaps(subject), (bubble.utterance_id, subject)
        # A second, independent source (the hand marks, when a real vision call drove the
        # layout) may draw a box slightly differently: only real coverage of it is a failure.
        for other in cross_checked:
            if other.kind not in ("face", "hand", "bird"):
                continue
            ox = min(page_box.right, other.box.right) - max(page_box.x, other.box.x)
            oy = min(page_box.bottom, other.box.bottom) - max(page_box.y, other.box.y)
            if ox > 0 and oy > 0:
                assert ox * oy <= 0.15 * other.box.width * other.box.height, (
                    bubble.utterance_id,
                    other,
                )
    for first, second in zip(result.bubbles, result.bubbles[1:] + result.bubbles[:1]):
        if first is not second:
            assert not _bounds(first).overlaps(_bounds(second)), "collision"


def _assert_tails_point_at_speakers(result, document, subjects):
    for bubble in result.bubbles:
        if bubble.kind != "speech":
            continue
        panel = _panel_of(document, bubble.panel_id)
        utterance = next(u for u in panel.utterances if u.utterance_id == bubble.utterance_id)
        faces = [
            f
            for f in _speaker_faces(document, subjects, panel)
            if same_character(utterance.speaker, f.character)
        ]
        if not faces:
            continue
        assert bubble.tail, f"speech without a tail: {bubble.utterance_id}"
        box = _bounds(bubble)
        tip = max(
            ((p.x, p.y) for p in bubble.tail),
            key=lambda p: math.hypot(p[0] - box.center[0], p[1] - box.center[1]),
        )

        def gap(face, tip=tip):
            dx = max(face.box.x - tip[0], 0, tip[0] - face.box.right)
            dy = max(face.box.y - tip[1], 0, tip[1] - face.box.bottom)
            return math.hypot(dx, dy)

        own = min(faces, key=gap)
        assert all(gap(own) <= gap(other) for other in faces)
        # The tail leaves the bubble toward the face: its tip is nearer than the bubble centre.
        centre_gap = math.hypot(
            max(own.box.x - box.center[0], 0, box.center[0] - own.box.right),
            max(own.box.y - box.center[1], 0, box.center[1] - own.box.bottom),
        )
        assert gap(own) <= centre_gap + 1e-9


def _assert_exports_without_review_note(result, document):
    state = PageLetteringState(
        version="page-lettering-v1", input=document, overrides={}, zone_overrides={}, layout=result
    )
    layout, warning = eligibility(
        {"lettering_mode": "overlay", "lettering": state.model_dump(mode="json"), "page_number": 1}
    )
    assert warning is None and layout is not None


@pytest.mark.parametrize(("name", "boxes"), BOX_SETS)
def test_saved_page_letters_completely_without_review(name, boxes):
    document, art, fed, marked = _load(name, boxes)

    result = layout_page(document, art_image=art, subjects=fed)

    _assert_clean_and_complete(result, document, fed, cross_checked=marked)
    _assert_tails_point_at_speakers(result, document, fed)
    _assert_exports_without_review_note(result, document)


@pytest.mark.parametrize("name", PAGES)
def test_layout_is_deterministic(name):
    document, art, subjects, _ = _load(name)

    first = layout_page(document, art_image=art, subjects=subjects)
    second = layout_page(document, art_image=art, subjects=subjects)

    assert first.model_dump(mode="json") == second.model_dump(mode="json")


@pytest.mark.parametrize("boxes", ["hand", "vision"])
def test_live_4_panel_one_narration_clears_the_head_and_brow(boxes):
    document, art, fed, marked = _load("live-4", boxes)
    head = next(s.box for s in marked if s.kind == "face")  # Panel 1: hat, brow and face.

    result = layout_page(document, art_image=art, subjects=fed)

    caption = next(b for b in result.bubbles if b.panel_id == document.panels[0].panel_id)
    assert caption.kind == "narration" and not _bounds(caption).overlaps(head)


def test_fixtures_stay_small():
    for path in CORPUS.iterdir():
        assert path.stat().st_size < 300_000, path


# Synthetic stress variants -------------------------------------------------------------


def _utterance(panel, kind, text, speaker=None):
    order = max((u.order for u in panel.utterances), default=-1) + 1
    panel.utterances.append(
        LetteringUtterance(
            utterance_id=f"{panel.panel_id}-extra-{order}",
            order=order,
            kind=kind,
            text=text,
            speaker=speaker,
        )
    )


LONGEST_CAPTION = (
    "Through the storm night the keeper watched dark water, hoping small light would shine."
)
OVER_BUDGET_CAPTION = (
    "Through the long storm night the keeper watched the dark water, hoping that one small "
    "brass light would lead the lost boat safely home."
)


def _set_narration(document, text):
    for panel in document.panels:
        for u in panel.utterances:
            if u.kind == "narration":
                u.text = text
    return document


def _longer_narration(document):
    # The 14-word caption budget enforced when the text is generated.
    assert len(LONGEST_CAPTION.split()) == 14
    return _set_narration(document, LONGEST_CAPTION)


def _three_utterances(document):
    for panel in document.panels:
        _utterance(panel, "speech", "Keep the lamp burning!", "Nia")
    return document


def _four_utterances(document):
    for panel in document.panels:
        _utterance(panel, "speech", "Keep the lamp burning!", "Nia")
        _utterance(panel, "caption", "The night grew longer.")
    return document


VARIANTS = {
    "longer-narration": _longer_narration,
    "three-per-panel": _three_utterances,
    "four-per-panel": _four_utterances,
}


@pytest.mark.parametrize("variant", sorted(VARIANTS))
@pytest.mark.parametrize("name", PAGES)
def test_stress_variant_still_letters_completely(name, variant):
    document, art, subjects, _ = _load(name)
    document = VARIANTS[variant](document)

    result = layout_page(document, art_image=art, subjects=subjects)

    _assert_clean_and_complete(result, document, subjects)
    _assert_exports_without_review_note(result, document)


@pytest.mark.parametrize("variant", sorted(VARIANTS))
@pytest.mark.parametrize("name", VISION_PAGES)
def test_stress_variant_with_real_vision_boxes_never_drops_a_line_or_covers_a_subject(
    name, variant
):
    # Real boxes are looser than hand marks (whole torsos, wide objects), so a crowded panel
    # may leave a line placed but flagged for review. It is never dropped, trimmed or hidden
    # over a face, hand or bird that the vision step found.
    document, art, fed, _ = _load(name, "vision")
    document = VARIANTS[variant](document)

    result = layout_page(document, art_image=art, subjects=fed)

    flagged = {i.utterance_id for i in result.issues}
    drawn = {}
    for bubble in result.bubbles:
        drawn.setdefault(bubble.utterance_id.split("~")[0], []).append(bubble)
    for panel in document.panels:
        for u in panel.utterances:
            parts = sorted(drawn[u.utterance_id], key=lambda b: b.utterance_id)
            assert " ".join(p.text for b in parts for p in b.lines).split() == u.text.split()
    hard = [s.box for s in fed if s.kind in ("face", "hand", "bird")]
    for bubble in result.bubbles:
        if bubble.utterance_id.split("~")[0] in flagged or bubble.utterance_id in flagged:
            continue
        assert not any(_bounds(bubble).overlaps(box) for box in hard)


@pytest.mark.parametrize("name", PAGES)
def test_no_calm_space_art_still_letters_every_line_clear_of_subjects(name):
    document, _, subjects, _ = _load(name)
    noise = np.random.default_rng(7).integers(0, 256, (960, 640, 3), dtype=np.uint8)
    buffer = BytesIO()
    Image.fromarray(noise).save(buffer, format="PNG")

    result = layout_page(document, art_image=buffer.getvalue(), subjects=subjects)

    # Detail everywhere leaves no calm slot, but no line may be dropped or cover a subject.
    placed = {b.utterance_id.split("~")[0] for b in result.bubbles}
    assert placed == {u.utterance_id for p in document.panels for u in p.utterances}
    for bubble in result.bubbles:
        assert not any(_bounds(bubble).overlaps(s.box) for s in subjects)


@pytest.mark.parametrize("name", PAGES)
def test_over_budget_narration_is_never_dropped_trimmed_or_hidden_over_a_subject(name):
    document, art, subjects, _ = _load(name)
    document = _set_narration(document, OVER_BUDGET_CAPTION)

    result = layout_page(document, art_image=art, subjects=subjects)

    flagged = {i.utterance_id for i in result.issues}
    drawn = {}
    for bubble in result.bubbles:
        drawn.setdefault(bubble.utterance_id.split("~")[0], []).append(bubble)
    for panel in document.panels:
        for u in panel.utterances:
            words = " ".join(
                part.text
                for b in sorted(drawn[u.utterance_id], key=lambda b: b.utterance_id)
                for part in b.lines
            )
            assert words.split() == u.text.split()
    for bubble in result.bubbles:
        if bubble.utterance_id.split("~")[0] in flagged or bubble.utterance_id in flagged:
            continue
        assert not any(_bounds(bubble).overlaps(s.box) for s in subjects)
