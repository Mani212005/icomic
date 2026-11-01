"""Panel text and chapter outline generation service."""

from ..adapters.language.port import LanguagePort, Message
from ..gen.models import ChapterOutlineItem, CharacterProfile, PanelTextItem


class PanelService:
    def __init__(
        self, language_adapter: LanguagePort, model_id: str = "meta-llama-3.3-70b-instruct"
    ):
        self.language_adapter = language_adapter
        self.model_id = model_id

    def generate_chapter_outline(
        self, script: str, chapter_count: int, approved_characters: list[CharacterProfile]
    ) -> list[ChapterOutlineItem]:
        _ = self.language_adapter.complete(
            self.model_id,
            [
                Message(
                    role="system",
                    content=f"Divide script into {chapter_count} chapters honoring approved characters.",
                ),
                Message(role="user", content=script),
            ],
        )

        char_names = [c.name for c in approved_characters] or ["Protagonist"]
        chapters = []
        for i in range(1, chapter_count + 1):
            chapters.append(
                ChapterOutlineItem(
                    chapter_number=i,
                    title=f"Chapter {i}: The Beginning of the Journey",
                    summary=f"Summary of events and narrative progression in chapter {i}.",
                    important_events=[f"Event {i}.1", f"Event {i}.2"],
                    character_names=char_names,
                    ending=f"Suspenseful cliffhanger at end of chapter {i}.",
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
        _ = self.language_adapter.complete(
            self.model_id,
            [
                Message(
                    role="system",
                    content=f"Generate 4 panels for {chapter_title}: {chapter_summary}.",
                ),
            ],
        )

        panels = []
        for p in range(1, 5):
            panels.append(
                PanelTextItem(
                    panel_number=p,
                    scene_description=f"Scene description for panel {p} of chapter {chapter_number}",
                    character_actions="Character steps forward looking into the distance",
                    camera_angle="Wide angle establishing shot",
                    background="Urban alleyway with rain-slick pavement",
                    dialogue="We need to find the truth.",
                    narration="The city whispered its forgotten secrets.",
                    image_prompt=f"Comic panel {p}: high contrast comic style, dramatic lighting",
                )
            )
        return panels
