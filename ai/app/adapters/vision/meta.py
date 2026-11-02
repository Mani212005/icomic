"""Meta vision adapter inert stub (deferred until API credits are available)."""

from .port import PaletteSuggestion, VisionPort


class MetaVisionAdapter(VisionPort):
    """Inert stub for Meta vision models.

    Raises a clear not-configured error upon initialization.
    No live API calls or API keys are read.
    """

    def __init__(self, *args, **kwargs):
        raise RuntimeError(
            "MetaVisionAdapter is not configured: live Meta API integration is deferred "
            "until API credits are provisioned. Use FakeVisionAdapter (default) via ADAPTER_VISION=fake."
        )

    def review(self, model: str, image_bytes: bytes, prompt: str) -> str:
        raise NotImplementedError("MetaVisionAdapter is inert and does not perform live calls.")

    def suggest_palette(
        self, model: str, image_bytes: bytes, panel_context: str
    ) -> PaletteSuggestion:
        raise NotImplementedError("MetaVisionAdapter is inert and does not perform live calls.")
