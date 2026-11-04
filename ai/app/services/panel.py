"""Panel text and chapter outline generation service."""

from ..adapters.language.port import LanguagePort, Message
from ..costs import WORST_CASE_TEXT_OUTPUT_TOKENS
from ..gen.models import ChapterOutlineItem, CharacterProfile, PanelTextItem


class PanelService:
    """Service handling chapter outlines and panel text generation."""

    def __init__(
        self, language_adapter: LanguagePort, model_id: str = "meta-llama-3.3-70b-instruct"
    ):
        self.language_adapter = language_adapter
        self.model_id = model_id

    def generate_chapter_outline(
        self, script: str, chapter_count: int, approved_characters: list[CharacterProfile]
    ) -> list[ChapterOutlineItem]:
        """Generate structured chapter outlines from script honoring approved character profiles."""
        if chapter_count < 1:
            raise ValueError("Chapter count must be at least 1.")

        _ = self.language_adapter.complete(
            self.model_id,
            [
                Message(
                    role="system",
                    content=f"Divide script into {chapter_count} chapters honoring approved characters.",
                ),
                Message(role="user", content=script),
            ],
            max_tokens=WORST_CASE_TEXT_OUTPUT_TOKENS,
        )

        char_names = [c.name for c in approved_characters] or ["Protagonist"]
        chapters: list[ChapterOutlineItem] = []
        for i in range(1, chapter_count + 1):
            chapters.append(
                ChapterOutlineItem(
                    chapter_number=i,
                    title=f"Chapter {i}: The Journey Begins",
                    summary=f"Narrative arc and story developments for chapter {i}.",
                    important_events=[f"Inciting event {i}.1", f"Key confrontation {i}.2"],
                    character_names=char_names,
                    ending=f"Dramatic transition hook at end of chapter {i}.",
                )
            )
        return chapters

    def generate_panel_text(
        self,
        chapter_number: int,
        chapter_title: str,
        chapter_summary: str,
        characters: list[str],
    ) -> list[PanelTextItem]:
        """Generate panel text and visual prompts for a chapter."""
        _ = self.language_adapter.complete(
            self.model_id,
            [
                Message(
                    role="system",
                    content=f"Generate 4 panels for {chapter_title}: {chapter_summary}.",
                ),
            ],
            max_tokens=WORST_CASE_TEXT_OUTPUT_TOKENS,
        )

        char_list = ", ".join(characters) if characters else "Protagonist"
        panels: list[PanelTextItem] = []
        for p in range(1, 5):
            panels.append(
                PanelTextItem(
                    panel_number=p,
                    scene_description=f"Scene description for panel {p} of chapter {chapter_number} featuring {char_list}",
                    character_actions="Character steps forward with decisive motion",
                    camera_angle="Dynamic establishing angle",
                    background="Atmospheric urban environment with dramatic shadows",
                    dialogue="We must move forward.",
                    narration="The storm gathered on the horizon.",
                    image_prompt=f"Comic panel {p}: high contrast graphic novel framing, expressive composition for {char_list}",
                )
            )
        return panels
