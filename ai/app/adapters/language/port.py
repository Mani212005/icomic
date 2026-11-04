"""Language port protocol for LLM interactions."""

from dataclasses import dataclass
from typing import Protocol, runtime_checkable


@dataclass
class Message:
    role: str
    content: str


@dataclass
class TokenUsage:
    input_tokens: int
    output_tokens: int
    total_tokens: int
    reasoning_tokens: int | None = None
    finish_reason: str | None = None
    cached_input_tokens: int = 0


@runtime_checkable
class LanguagePort(Protocol):
    """Protocol defining LLM text generation and token estimation interface."""

    def complete(self, model: str, messages: list[Message], **kwargs) -> str:
        """Execute a text completion request."""
        ...

    def estimate_tokens(self, messages: list[Message]) -> TokenUsage:
        """Estimate token usage for given messages."""
        ...
