"""Vision port protocol for multimodal image review and palette suggestions."""

from dataclasses import dataclass
from typing import Protocol, runtime_checkable


@dataclass
class ColourItem:
    region_name: str
    hex_color: str
    label: str


@dataclass
class PaletteSuggestion:
    theme: str
    palette: list[ColourItem]


@runtime_checkable
class VisionPort(Protocol):
    """Protocol defining multimodal image review and palette suggestion interface."""

    def review(self, model: str, image_bytes: bytes, prompt: str, **kwargs) -> str:
        """Review an image with a multimodal model."""
        ...

    def suggest_palette(
        self, model: str, image_bytes: bytes, panel_context: str, **kwargs
    ) -> PaletteSuggestion:
        """Suggest colour palette based on line art and panel context."""
        ...
