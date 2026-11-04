"""Prompt pre-moderation service."""

import math

from ..adapters.language.port import LanguagePort, Message
from ..costs import STORY_INPUT_TOKEN_LIMIT
from .pacing import SOURCE_WORDS_PER_CALL, split_text


class ModerationService:
    """Service pre-screening prompts before dispatching to image generation."""

    def __init__(self, language_adapter: LanguagePort, model_id: str = ""):
        self.language_adapter = language_adapter
        self.model_id = model_id

    def moderate_prompt(self, prompt: str) -> tuple[bool, str | None]:
        """Pre-screen prompt against content moderation guidelines."""
        clean_prompt = prompt.strip().lower()
        prohibited = ["nsfw", "hate speech", "extreme violence", "csam", "illegal"]
        for word in prohibited:
            if word in clean_prompt:
                return True, f"Prompt blocked by pre-moderation: keyword '{word}' is prohibited."

        for source in split_text(
            prompt, max(1, math.ceil(len(prompt.split()) / SOURCE_WORDS_PER_CALL))
        ):
            if len(source.encode()) + 100 > STORY_INPUT_TOKEN_LIMIT:
                raise ValueError("Moderation source exceeds the bounded input budget.")
            self.language_adapter.complete(
                self.model_id,
                [
                    Message(
                        role="system",
                        content="Evaluate prompt for safety against Meta content guidelines.",
                    ),
                    Message(role="user", content=source),
                ],
                max_tokens=32,
            )

        return False, None
