"""Colour palette suggestion service."""

from ..adapters.vision.port import VisionPort
from ..gen.models import ColourRegionSuggestion


class ColourService:
    def __init__(
        self, vision_adapter: VisionPort, model_id: str = "meta-llama-3.2-11b-vision-instruct"
    ):
        self.vision_adapter = vision_adapter
        self.model_id = model_id

    def suggest_palette(
        self, line_art_image_bytes: bytes, panel_context: str
    ) -> tuple[str, list[ColourRegionSuggestion]]:
        suggestion = self.vision_adapter.suggest_palette(
            model=self.model_id,
            image_bytes=line_art_image_bytes,
            panel_context=panel_context,
        )

        regions = [
            ColourRegionSuggestion(
                region_name=c.region_name,
                hex_color=c.hex_color,
                label=c.label,
            )
            for c in suggestion.palette
        ]
        return suggestion.theme, regions
