"""Deterministic fake language adapter for hermetic testing and development."""

import hashlib

from .port import LanguagePort, Message, TokenUsage


class FakeLanguageAdapter(LanguagePort):
    """Deterministic in-memory language model adapter with no external calls."""

    def __init__(self, default_response: str = "Deterministic test response"):
        self.default_response = default_response

    def complete(self, model: str, messages: list[Message], **kwargs) -> str:
        prompt_concat = "".join(f"{m.role}:{m.content}\n" for m in messages)
        h = hashlib.sha256(prompt_concat.encode("utf-8")).hexdigest()[:8]
        return f"[FakeLanguageAdapter hash={h}] {self.default_response}"

    def estimate_tokens(self, messages: list[Message]) -> TokenUsage:
        chars = sum(len(m.content) for m in messages)
        est_tokens = max(1, chars // 4)
        return TokenUsage(input_tokens=est_tokens, output_tokens=50, total_tokens=est_tokens + 50)
