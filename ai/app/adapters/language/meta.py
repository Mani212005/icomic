"""Meta language adapter inert stub (deferred until API credits are available)."""

from .port import LanguagePort, Message, TokenUsage


class MetaLanguageAdapter(LanguagePort):
    """Inert stub for Meta language models.

    Raises a clear not-configured error upon initialization.
    No live API calls or API keys are read.
    """

    def __init__(self, *args, **kwargs):
        raise RuntimeError(
            "MetaLanguageAdapter is not configured: live Meta API integration is deferred "
            "until API credits are provisioned. Use FakeLanguageAdapter (default) via ADAPTER_LANGUAGE=fake."
        )

    def complete(self, model: str, messages: list[Message], **kwargs) -> str:
        raise NotImplementedError("MetaLanguageAdapter is inert and does not perform live calls.")

    def estimate_tokens(self, messages: list[Message]) -> TokenUsage:
        raise NotImplementedError("MetaLanguageAdapter is inert and does not perform live calls.")
