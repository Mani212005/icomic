"""Deterministic fake vision adapter for hermetic testing and development."""

from .port import ColourItem, PaletteSuggestion, SubjectBox, VisionPort


class FakeVisionAdapter(VisionPort):
    """Deterministic vision model adapter providing fixed review and palette responses."""

    def __init__(
        self,
        replay_adapter: VisionPort | None = None,
        subject_fixture: list[SubjectBox] | None = None,
    ):
        self.replay_adapter = replay_adapter
        self.subject_fixture = subject_fixture
        self.last_usage = None
        self.subject_calls = 0

    def detect_subjects(
        self, model: str, image_bytes: bytes, characters: list[str], **kwargs
    ) -> list[SubjectBox]:
        """Boxes from a fixture, else one deterministic face per named character."""
        self.subject_calls += 1
        if self.subject_fixture is not None:
            return list(self.subject_fixture)
        return [
            SubjectBox("face", 0.12 + 0.3 * i, 0.12, 0.18, 0.1, 0.9, name)
            for i, name in enumerate(characters[:3])
        ] or [SubjectBox("object", 0.4, 0.4, 0.2, 0.1, 0.9)]

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
        style_bible = kwargs.get("style_bible")
        if style_bible is not None and hasattr(style_bible, "palette"):
            return PaletteSuggestion(
                theme=style_bible.palette.theme,
                palette=[
                    ColourItem(
                        region_name=k,
                        hex_color=v,
                        label=k.replace("_", " ").title(),
                    )
                    for k, v in style_bible.palette.suggested_regions.items()
                ],
            )
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
