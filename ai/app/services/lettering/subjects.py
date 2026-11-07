"""Subject boxes for lettering: one bounded vision call per page, never fatal."""

from __future__ import annotations

import logging
import os
from collections.abc import Sequence

from ...adapters.vision.port import VisionPort
from ...gen.models import LetteringSubject, LetteringUnitRect
from .art_safety import Subject
from .geometry import Box

logger = logging.getLogger(__name__)


def subject_detection_enabled(environ: dict[str, str] | None = None) -> bool:
    """On by default whenever overlay lettering is on; one switch can override it."""
    environ = os.environ if environ is None else environ
    explicit = environ.get("LETTERING_SUBJECT_DETECTION_ENABLED", "").strip()
    if explicit:
        return explicit.lower() == "true"
    return environ.get("LETTERING_OVERLAY_ENABLED", "false").lower() == "true"


def to_art_subjects(subjects: Sequence[LetteringSubject]) -> list[Subject]:
    """Wire boxes (page-normalized) as the layout engine's subjects."""
    return [
        Subject(
            Box(s.rect.x, s.rect.y, s.rect.width, s.rect.height),
            s.kind,
            s.confidence,
            s.character,
        )
        for s in subjects
    ]


def detect_page_subjects(
    vision: VisionPort,
    image_bytes: bytes,
    characters: list[str],
    *,
    model: str = "",
    hashed_user_id: str | None = None,
) -> tuple[list[LetteringSubject], str]:
    """Ask the vision adapter for subject boxes once; fall back to heuristics on any failure.

    There is no retry. A refused, failed, malformed or empty answer returns no boxes and the
    source `fallback`, so layout keeps using its colour and detail heuristics and the export
    is never blocked by this call.
    """
    try:
        boxes = vision.detect_subjects(
            model,
            image_bytes,
            characters,
            **({"hashed_user_id": hashed_user_id} if hashed_user_id else {}),
        )
        subjects = [
            LetteringSubject(
                kind=b.kind,
                rect=LetteringUnitRect(
                    x=b.x, y=b.y, width=max(b.width, 1e-9), height=max(b.height, 1e-9)
                ),
                confidence=b.confidence,
                character=b.character,
            )
            for b in boxes
        ]
    except Exception as exc:  # noqa: BLE001 - a provider fault must never fail the page
        # Includes refusals by the cost guard; whatever was spent stays on the ledger.
        logger.warning(
            "Lettering subject detection fell back to heuristics: %s", type(exc).__name__
        )
        return [], "fallback"
    return (subjects, "vision") if subjects else ([], "fallback")
