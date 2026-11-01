"""Meta vision adapter (deferred until API credits are available)."""

import os

from .port import PaletteSuggestion, VisionPort


class MetaVisionAdapter(VisionPort):
    def __init__(self, api_key: str | None = None):
        self.api_key = api_key or os.getenv("META_API_KEY")
        if not self.api_key:
            raise RuntimeError(
                "MetaVisionAdapter requires META_API_KEY. Use FakeVisionAdapter for local tests."
            )

    def review(self, model: str, image_bytes: bytes, prompt: str) -> str:
        raise NotImplementedError("Live Meta Vision API is deferred until credits are available.")

    def suggest_palette(
        self, model: str, image_bytes: bytes, panel_context: str
    ) -> PaletteSuggestion:
        raise NotImplementedError("Live Meta Vision API is deferred until credits are available.")
