"""Deterministic fake vision adapter for hermetic testing and development."""

from .port import ColourItem, PaletteSuggestion, VisionPort


class FakeVisionAdapter(VisionPort):
    """Deterministic vision model adapter providing fixed review and palette responses."""

    def __init__(self, replay_adapter: VisionPort | None = None):
        self.replay_adapter = replay_adapter
        self.last_usage = None

    def review(self, model: str, image_bytes: bytes, prompt: str, **kwargs) -> str:
        if self.replay_adapter is not None:
            if hasattr(self.replay_adapter, "last_usage"):
                self.replay_adapter.last_usage = None
            try:
                return self.replay_adapter.review(model, image_bytes, prompt, **kwargs)
            finally:
                self.last_usage = getattr(self.replay_adapter, "last_usage", None)
        return "OK: Image passes quality and composition checks."

    def suggest_palette(
        self, model: str, image_bytes: bytes, panel_context: str, **kwargs
    ) -> PaletteSuggestion:
        if self.replay_adapter is not None:
            if hasattr(self.replay_adapter, "last_usage"):
                self.replay_adapter.last_usage = None
            try:
                return self.replay_adapter.suggest_palette(
                    model, image_bytes, panel_context, **kwargs
                )
            finally:
                self.last_usage = getattr(self.replay_adapter, "last_usage", None)
        return PaletteSuggestion(
            theme="Neo-Noir Sunset",
            palette=[
                ColourItem(region_name="sky_background", hex_color="#2A2356", label="Night Sky"),
                ColourItem(
                    region_name="character_coat", hex_color="#D1495B", label="Ruby Trenchcoat"
                ),
                ColourItem(
                    region_name="character_hair", hex_color="#2E4057", label="Midnight Blue Hair"
                ),
                ColourItem(region_name="neon_sign", hex_color="#EDAE49", label="Amber Neon Glow"),
            ],
        )
