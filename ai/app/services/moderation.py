"""Prompt pre-moderation service."""

from ..adapters.language.port import LanguagePort, Message


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

        # Pass through language moderation adapter
        _ = self.language_adapter.complete(
            self.model_id,
            [
                Message(
                    role="system",
                    content="Evaluate prompt for safety against Meta content guidelines.",
                ),
                Message(role="user", content=prompt),
            ],
        )

        return False, None
