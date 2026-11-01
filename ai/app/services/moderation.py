"""Prompt pre-moderation service."""

from ..adapters.language.port import LanguagePort, Message


class ModerationService:
    def __init__(self, language_adapter: LanguagePort, model_id: str = "meta-llama-guard-3-8b"):
        self.language_adapter = language_adapter
        self.model_id = model_id

    def moderate_prompt(self, prompt: str) -> tuple[bool, str | None]:
        # Pre-screen prompt for blocked content
        lower = prompt.lower()
        blocked_keywords = ["nsfw", "hate speech", "extreme violence", "csam", "illegal"]
        for kw in blocked_keywords:
            if kw in lower:
                return True, f"Prompt blocked by pre-moderation: keyword '{kw}' is prohibited."

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
