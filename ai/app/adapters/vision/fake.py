"""Deterministic fake vision adapter for hermetic testing and development."""

from .port import ColourItem, PaletteSuggestion, VisionPort


class FakeVisionAdapter(VisionPort):
    """Deterministic vision model adapter providing fixed review and palette responses."""

    def review(self, model: str, image_bytes: bytes, prompt: str) -> str:
        return "OK: Image passes quality and composition checks."

    def suggest_palette(
        self, model: str, image_bytes: bytes, panel_context: str
    ) -> PaletteSuggestion:
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
