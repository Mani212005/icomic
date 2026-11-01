"""Vision port protocol for multimodal image review and palette suggestions."""

from dataclasses import dataclass
from typing import Protocol


@dataclass
class ColourItem:
    region_name: str
    hex_color: str
    label: str


@dataclass
class PaletteSuggestion:
    theme: str
    palette: list[ColourItem]


class VisionPort(Protocol):
    def review(self, model: str, image_bytes: bytes, prompt: str) -> str:
        """Review an image with a multimodal model."""
        ...

    def suggest_palette(
        self, model: str, image_bytes: bytes, panel_context: str
    ) -> PaletteSuggestion:
        """Suggest colour palette based on line art and panel context."""
        ...
