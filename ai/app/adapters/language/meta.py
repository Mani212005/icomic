"""Meta language adapter (deferred until API credits are available)."""

import os

from .port import LanguagePort, Message, TokenUsage


class MetaLanguageAdapter(LanguagePort):
    def __init__(self, api_key: str | None = None):
        self.api_key = api_key or os.getenv("META_API_KEY")
        if not self.api_key:
            raise RuntimeError(
                "MetaLanguageAdapter requires META_API_KEY. Use FakeLanguageAdapter for local tests."
            )

    def complete(self, model: str, messages: list[Message], **kwargs) -> str:
        raise NotImplementedError("Live Meta API is deferred until credits are available.")

    def estimate_tokens(self, messages: list[Message]) -> TokenUsage:
        chars = sum(len(m.content) for m in messages)
        tokens = chars // 4
        return TokenUsage(input_tokens=tokens, output_tokens=0, total_tokens=tokens)
