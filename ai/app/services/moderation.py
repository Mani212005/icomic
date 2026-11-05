"""Prompt pre-moderation service."""

import math

from ..adapters.language.port import LanguagePort, Message
from ..costs import GENERATION_POLICIES, STORY_INPUT_TOKEN_LIMIT
from ..provider_output import ProviderOutputError
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
            messages = [
                Message(
                    role="system",
                    content="Evaluate prompt for safety against Meta content guidelines. Reply concisely: SAFE, or BLOCK followed by a brief reason. Do not repeat the prompt or write an analysis.",
                ),
                Message(role="user", content=source),
            ]
            if sum(len(m.content.encode()) for m in messages) > STORY_INPUT_TOKEN_LIMIT:
                raise ValueError("Moderation source exceeds the bounded input budget.")
            decision = self.language_adapter.complete(
                self.model_id,
                messages,
                **GENERATION_POLICIES["moderation"].options(),
            )
            verdict = decision.strip().rstrip(".").upper()
            if verdict.startswith("BLOCK"):
                return True, "Prompt blocked by pre-moderation: " + decision.strip()
            if verdict != "SAFE":
                raise ProviderOutputError("moderation", "invalid_safety_decision")

        return False, None
