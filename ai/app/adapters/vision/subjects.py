"""Prompt, downscaling and defensive parsing for the lettering subject-box vision call."""

from __future__ import annotations

import json
import math
import re
from io import BytesIO

from PIL import Image

from ...provider_output import ProviderOutputError
from .port import SubjectBox

SUBJECT_KINDS = ("face", "hand", "bird", "object")
# The vision call never sees more than this many pixels along the long edge.
MAX_VISION_SIDE = 1024
MAX_SUBJECTS = 40
MIN_SIDE = 0.01
# A single box covering most of the page says nothing about where lettering may go.
MAX_SIDE = 0.9


def downscale_for_vision(image_bytes: bytes, max_side: int = MAX_VISION_SIDE) -> bytes:
    """Re-encode the page as a JPEG whose long edge is at most `max_side` pixels."""
    with Image.open(BytesIO(image_bytes)) as source:
        image = source.convert("RGB")
    if max(image.size) > max_side:
        image.thumbnail((max_side, max_side), Image.Resampling.LANCZOS)
    out = BytesIO()
    image.save(out, format="JPEG", quality=85)
    return out.getvalue()


def subjects_prompt(characters: list[str]) -> str:
    names = ", ".join(characters) if characters else "none named"
    return (
        "You are marking up a finished comic page so speech bubbles and captions can be "
        "placed without covering anything important. Find every human face (include the "
        "hair, hat or hood), every hand, every bird, and any other key object a reader "
        "must see (lamps, boats, signs). Respond strictly in valid JSON, with boxes "
        "normalized to the whole image (0 to 1, origin top left):\n"
        '{"subjects": [{"kind": "face", "character": "Name or null", "confidence": 0.9, '
        '"box": [x, y, width, height]}]}\n'
        'Use only these kinds: "face", "hand", "bird", "object". For a face or hand, set '
        '"character" to the matching name from this list, or null if unsure: '
        f"{names}. Boxes must be tight."
    )


def _number(value, low: float, high: float) -> float:
    if isinstance(value, bool) or not isinstance(value, (int, float)):
        raise TypeError("not a number")
    value = float(value)
    if not math.isfinite(value) or not low <= value <= high:
        raise ValueError("out of range")
    return value


def parse_subjects(content: str, characters: list[str]) -> list[SubjectBox]:
    """Validate a model reply. Junk raises ProviderOutputError; it is never repaired.

    Individual boxes that fail validation are dropped. A reply with no valid box at all is
    junk, because an empty page would silently disable every safety heuristic.
    """
    cleaned = content.strip()
    if "```" in cleaned:
        match = re.search(r"```(?:json)?\s*([\s\S]*?)\s*```", cleaned)
        if match:
            cleaned = match.group(1).strip()
    try:
        parsed = json.loads(cleaned)
    except (json.JSONDecodeError, ValueError) as exc:
        raise ProviderOutputError("subjects", "invalid_or_incomplete_json") from exc
    raw = parsed.get("subjects") if isinstance(parsed, dict) else None
    if not isinstance(raw, list):
        raise ProviderOutputError("subjects", "schema_mismatch")
    known = {name.casefold(): name for name in characters}
    boxes: list[SubjectBox] = []
    for entry in raw[:MAX_SUBJECTS]:
        try:
            if not isinstance(entry, dict) or entry.get("kind") not in SUBJECT_KINDS:
                raise ValueError("kind")
            raw_box = entry.get("box")
            if not isinstance(raw_box, list) or len(raw_box) != 4:
                raise ValueError("box")
            x, y, width, height = (_number(v, -0.02, 1.02) for v in raw_box)
            x, y = max(0.0, x), max(0.0, y)
            width, height = min(width, 1.0 - x), min(height, 1.0 - y)
            if not (MIN_SIDE <= width <= MAX_SIDE and MIN_SIDE <= height <= MAX_SIDE):
                raise ValueError("size")
            confidence = _number(entry.get("confidence"), 0.0, 1.0)
            character = entry.get("character")
            if isinstance(character, str) and character.strip():
                character = known.get(character.strip().casefold())
            else:
                character = None
        except (TypeError, ValueError):
            continue
        boxes.append(SubjectBox(entry["kind"], x, y, width, height, confidence, character))
    if not boxes:
        raise ProviderOutputError("subjects", "schema_mismatch")
    return boxes
