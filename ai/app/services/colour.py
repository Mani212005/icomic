"""Colour palette suggestion service."""

from ..adapters.vision.port import VisionPort
from ..costs import GENERATION_POLICIES
from ..gen.models import ColourRegionSuggestion


class ColourService:
    """Service suggesting harmonious color palettes using multimodal vision models."""

    def __init__(self, vision_adapter: VisionPort, model_id: str = ""):
        self.vision_adapter = vision_adapter
        self.model_id = model_id

    def suggest_palette(
        self,
        line_art_image_bytes: bytes,
        panel_context: str,
        style: str | None = None,
    ) -> tuple[str, list[ColourRegionSuggestion]]:
        """Suggest region-based colour palette based on line art, context, and style bible."""
        from .style_bible import resolve_style_bible

        style_bible = resolve_style_bible(style)
        suggestion = self.vision_adapter.suggest_palette(
            model=self.model_id,
            image_bytes=line_art_image_bytes,
            panel_context=panel_context,
            style_bible=style_bible,
            **GENERATION_POLICIES["palette"].options(),
        )

        regions = [
            ColourRegionSuggestion(
                region_name=item.region_name,
                hex_color=item.hex_color,
                label=item.label,
            )
            for item in suggestion.palette
        ]
        return suggestion.theme, regions
