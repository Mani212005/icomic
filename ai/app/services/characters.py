"""Bound automatic reference work while preserving every extracted profile."""

import re

from ..gen.models import CharacterProfile

MAX_MAIN_CHARACTERS = 6


def assign_reference_roles(profiles: list[CharacterProfile], script: str) -> int:
    # Repeated names in the complete source outrank cameos; original extraction
    # order breaks ties. No additional provider call or story truncation is needed.
    ranked = sorted(
        enumerate(profiles),
        key=lambda pair: (
            -len(
                re.findall(r"(?<!\w)" + re.escape(pair[1].name) + r"(?!\w)", script, re.IGNORECASE)
            ),
            pair[0],
        ),
    )
    main = {index for index, _ in ranked[:MAX_MAIN_CHARACTERS]}
    for index, profile in enumerate(profiles):
        profile.visual_attributes = {
            **(profile.visual_attributes or {}),
            "reference_role": "main" if index in main else "extra",
        }
    return len(profiles) - len(main)
