"""Bounded chapter outlines and page-sized panel generation."""

import math

from pydantic import TypeAdapter

from ..adapters.language.port import LanguagePort
from ..gen.models import (
    ChapterOutlineItem,
    CharacterProfile,
    LetteringDocument,
    LetteringPanel,
    LetteringUnitRect,
    LetteringUtterance,
    PanelTextItem,
)
from ..provider_output import ProviderOutputError
from .image_prompts import IMAGE_ONLY_INSTRUCTIONS
from .lettering import layout_page
from .pacing import PANELS_PER_PAGE, SOURCE_WORDS_PER_CALL, split_text
from .structured import complete_json

OVERLAY_SPEECH_WORDS = 12
OVERLAY_CAPTION_WORDS = 14


def _spoken_text(line: str) -> str:
    """The lettered words of a speech turn, without its leading `Speaker:`."""
    before, colon, after = line.partition(":")
    if colon and before.strip() and len(before) <= 80 and len(before.split()) <= 4:
        return after.strip()
    return line


def _with_overlay_text_budgets(schema: dict) -> dict:
    """Add JSON Schema patterns so overlay word limits are checked before parsing."""
    properties = schema["$defs"]["PanelTextItem"]["properties"]
    word = r"[^\s\r\n]+"
    gap = r"[^\S\r\n]+"
    # An optional leading `Name:` (up to four tokens) names the speaker and is not lettered.
    speaker = rf"(?:[^\s:\r\n]+(?:{gap}[^\s:\r\n]+){{0,3}}:{gap})?"
    speech_line = rf"{speaker}{word}(?:{gap}{word}){{0,{OVERLAY_SPEECH_WORDS - 1}}}"
    properties["dialogue"]["pattern"] = rf"^(?:{speech_line}(?:\r?\n{speech_line})?)?$"
    properties["dialogue"]["description"] = (
        "At most two newline-separated speech turns, each starting with the speaking "
        "character's exact name and a colon, then no more than 12 whitespace-separated words."
    )
    caption_word = r"\S+"
    properties["narration"]["pattern"] = (
        rf"^(?:{caption_word}(?:\s+{caption_word}){{0,{OVERLAY_CAPTION_WORDS - 1}}})?$"
    )
    properties["narration"]["description"] = (
        "One caption with no more than 14 whitespace-separated words."
    )
    return schema


def _overlay_page_document(panels: list[PanelTextItem], page_number: int) -> LetteringDocument:
    """Build the same default panel rectangles and zones as the Go page source."""
    count = len(panels)
    columns = 1 if count <= 2 else 2
    rows = math.ceil(count / columns)
    width = (0.94 - (columns - 1) * 0.015) / columns
    height = (0.96 - (rows - 1) * 0.01) / rows
    result = []
    for index, item in enumerate(panels):
        row, column = divmod(index, columns)
        panel_width = 0.94 if index == count - 1 and column == 0 else width
        panel_id = f"overlay-validation-{page_number}-{item.panel_number}"
        units = []
        if item.narration.strip():
            units.append(
                LetteringUtterance(
                    utterance_id=f"{panel_id}-narration",
                    order=0,
                    kind="narration",
                    text=item.narration,
                )
            )
        for line_index, line in enumerate(item.dialogue.splitlines()):
            if not line.strip():
                continue
            text = line
            if ":" in line:
                before, after = line.split(":", 1)
                if len(before) <= 80 and before.strip():
                    text = after.strip()
            units.append(
                LetteringUtterance(
                    utterance_id=f"{panel_id}-dialogue-{line_index}",
                    order=len(units),
                    kind="speech",
                    text=text,
                )
            )
        characters = sum(len(unit.text) for unit in units)
        band = min(
            0.80,
            0.55 + max(0, characters - 50) * 0.001 + max(0, len(units) - 1) * 0.10,
        )
        rect = LetteringUnitRect(
            x=0.03 + column * (width + 0.015),
            y=0.02 + row * (height + 0.01),
            width=panel_width,
            height=height,
        )
        result.append(
            LetteringPanel(
                panel_id=panel_id,
                order=index,
                rect=rect,
                reserved_zones=(
                    [LetteringUnitRect(x=0.04, y=0.04, width=0.92, height=band)] if units else []
                ),
                utterances=units,
            )
        )
    return LetteringDocument(
        version="lettering-v1",
        page_id=f"overlay-validation-page-{page_number}",
        art_mode="text_free",
        page_aspect=2 / 3,
        panels=result,
    )


def _validate_overlay_fit(panels: list[PanelTextItem], page_number: int) -> None:
    layout = layout_page(_overlay_page_document(panels, page_number))
    blocking = {"collision", "outside_reserved_zone", "overflow"}
    if any(str(issue.flag) in blocking for issue in layout.issues):
        raise ValueError(
            "Overlay text does not fit its A4 reserved zones at the documented 18 pt minimum. "
            "Rewrite within the speech and caption word budgets while preserving the story's essential meaning."
        )


class PanelService:
    """Generate every chapter and panel without a single large model response."""

    def __init__(self, language_adapter: LanguagePort, model_id: str = ""):
        self.language_adapter = language_adapter
        self.model_id = model_id

    def generate_chapter_outline(
        self, script: str, chapter_count: int, approved_characters: list[CharacterProfile]
    ) -> list[ChapterOutlineItem]:
        if chapter_count < 1:
            raise ValueError("Chapter count must be at least 1.")
        chapters = []
        for number, source in enumerate(split_text(script, chapter_count), 1):
            # Large user-selected chapters receive several bounded outline calls.
            # Combine all their summaries instead of dropping late story events.
            parts = []
            for chunk in split_text(
                source, max(1, math.ceil(len(source.split()) / SOURCE_WORDS_PER_CALL))
            ):
                item = complete_json(
                    self.language_adapter,
                    self.model_id,
                    "outline",
                    {
                        "source": chunk,
                        "chapter_number": number,
                        "characters": [c.name for c in approved_characters],
                    },
                    ChapterOutlineItem.model_json_schema(),
                )
                parts.append(ChapterOutlineItem.model_validate(item))
            first = parts[0]
            first.chapter_number = number
            first.summary = "\n".join(p.summary for p in parts)
            first.important_events = [event for p in parts for event in p.important_events]
            first.ending = parts[-1].ending
            first.character_names = [c.name for c in approved_characters]
            chapters.append(first)
        return chapters

    def generate_panel_text(
        self,
        chapter_number: int,
        chapter_title: str,
        chapter_summary: str,
        characters: list[str],
        panel_count: int = 4,
        chapter_script: str = "",
        overlay_lettering: bool = False,
    ) -> list[PanelTextItem]:
        if panel_count < 1:
            raise ValueError("Panel count must be at least 1.")
        page_count = math.ceil(panel_count / PANELS_PER_PAGE)
        sources = split_text(chapter_script or chapter_summary, page_count)
        panels = []
        schema = TypeAdapter(list[PanelTextItem])
        for page_index, source in enumerate(sources):
            count = (
                page_index + 1
            ) * panel_count // page_count - page_index * panel_count // page_count
            output_schema = schema.json_schema()
            if overlay_lettering:
                output_schema = _with_overlay_text_budgets(output_schema)
            try:
                result = complete_json(
                    self.language_adapter,
                    self.model_id,
                    "panels",
                    {
                        "source": source,
                        "chapter_number": chapter_number,
                        "chapter_title": chapter_title,
                        "approved_outline": chapter_summary,
                        "page_number": page_index + 1,
                        "panel_count": count,
                        "characters": characters,
                    },
                    output_schema,
                    overlay_lettering=overlay_lettering,
                )
            except ProviderOutputError as exc:
                if overlay_lettering and exc.kind == "overlay_text_budget":
                    raise ValueError(
                        "Overlay text exceeds 12 words per speech turn or 14 words per narration caption; "
                        "rewrite without dropping essential story meaning."
                    ) from exc
                raise
            page_panels = schema.validate_python(result)
            if len(page_panels) != count:
                raise ValueError(
                    f"Expected {count} panels on page {page_index + 1}, received {len(page_panels)}"
                )
            for item in page_panels:
                dialogue_turns = [line for line in item.dialogue.splitlines() if line.strip()]
                if overlay_lettering and (
                    len(dialogue_turns) > 2
                    or any(
                        len(_spoken_text(line).split()) > OVERLAY_SPEECH_WORDS
                        for line in dialogue_turns
                    )
                    or len(item.narration.split()) > OVERLAY_CAPTION_WORDS
                ):
                    raise ValueError(
                        "Overlay text exceeds 12 words per speech turn or 14 words per narration caption; "
                        "rewrite without dropping essential story meaning."
                    )
                item.panel_number = len(panels) + 1
                item.image_prompt = (
                    f"{item.image_prompt.rstrip()}\n\n{IMAGE_ONLY_INSTRUCTIONS}".strip()
                )
                panels.append(item)
            if overlay_lettering:
                _validate_overlay_fit(page_panels, page_index + 1)
        return panels
