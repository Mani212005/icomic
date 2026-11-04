"""Bounded chapter outlines and page-sized panel generation."""

import math

from pydantic import TypeAdapter

from ..adapters.language.port import LanguagePort
from ..gen.models import ChapterOutlineItem, CharacterProfile, PanelTextItem
from .pacing import PANELS_PER_PAGE, SOURCE_WORDS_PER_CALL, split_text
from .structured import complete_json


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
                schema.json_schema(),
            )
            page_panels = schema.validate_python(result)
            if len(page_panels) != count:
                raise ValueError(
                    f"Expected {count} panels on page {page_index + 1}, received {len(page_panels)}"
                )
            for item in page_panels:
                item.panel_number = len(panels) + 1
                panels.append(item)
        return panels
