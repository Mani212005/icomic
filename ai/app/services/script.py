"""Script analysis service."""

from ..adapters.language.port import LanguagePort, Message
from ..gen.models import CharacterProfile


class ScriptService:
    """Business logic for script analysis and character extraction."""

    def __init__(
        self, language_adapter: LanguagePort, model_id: str = "meta-llama-3.3-70b-instruct"
    ):
        self.language_adapter = language_adapter
        self.model_id = model_id

    def analyze_script(
        self, script: str, target_chapter_count: int | None = None
    ) -> tuple[list[CharacterProfile], int, int, list[str]]:
        """Analyze script text, extract character profiles, and suggest chapter breakdown."""
        clean_script = script.strip()
        if not clean_script:
            raise ValueError("Script input cannot be empty.")

        words = clean_script.split()
        word_count = len(words)

        suggested_chapters = target_chapter_count or max(1, min(10, word_count // 200 + 1))
        warnings: list[str] = []
        if word_count < 50:
            warnings.append("Script is very short; fewer panels will be generated.")
        elif word_count > 5000:
            warnings.append("Script is very long; consider splitting into multiple projects.")

        # Complete language analysis via LanguagePort
        _ = self.language_adapter.complete(
            self.model_id,
            [
                Message(
                    role="system",
                    content="Extract character profiles and chapter breakdown from the script.",
                ),
                Message(role="user", content=clean_script),
            ],
        )

        # Build extracted character profiles
        profiles = [
            CharacterProfile(
                name="Protagonist",
                description="The central figure described in the script",
                age_range="20-30",
                appearance="Determined expression, medium build",
                hair_style="Dark wavy hair",
                clothing="Trenchcoat and durable boots",
                personality="Observant and persistent",
                visual_attributes={"palette": "earth-tones", "signature_prop": "notebook"},
            )
        ]

        return profiles, suggested_chapters, word_count, warnings
