"""Script analysis service."""

import math

from pydantic import TypeAdapter

from ..adapters.language.port import LanguagePort
from ..gen.models import CharacterProfile
from .pacing import SOURCE_WORDS_PER_CALL, chapter_count, split_text
from .structured import complete_json


class ScriptService:
    """Extract characters in bounded source chunks and preserve every chunk."""

    def __init__(self, language_adapter: LanguagePort, model_id: str = ""):
        self.language_adapter = language_adapter
        self.model_id = model_id

    def analyze_script(
        self, script: str, target_chapter_count: int | None = None
    ) -> tuple[list[CharacterProfile], int, int, list[str]]:
        if not script.strip():
            raise ValueError("Script input cannot be empty.")
        word_count = len(script.split())
        profiles: dict[str, CharacterProfile] = {}
        schema = TypeAdapter(list[CharacterProfile])
        for source in split_text(script, max(1, math.ceil(word_count / SOURCE_WORDS_PER_CALL))):
            extracted = complete_json(
                self.language_adapter,
                self.model_id,
                "characters",
                {"source": source},
                schema.json_schema(),
            )
            for profile in schema.validate_python(extracted):
                profiles.setdefault(profile.name.casefold(), profile)
        if not profiles:
            raise ValueError("Script analysis returned no character profiles.")
        warnings = (
            ["Script is very short; fewer panels will be generated."] if word_count < 50 else []
        )
        return (
            list(profiles.values()),
            target_chapter_count or chapter_count(word_count),
            word_count,
            warnings,
        )
