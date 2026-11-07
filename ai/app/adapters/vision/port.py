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


@dataclass(frozen=True)
class SubjectBox:
    """A face, hand, bird or key object on a painted page, normalized to the page image.

    `x`, `y`, `width` and `height` are fractions of the whole image with a top-left origin.
    `character` names the character a face or hand belongs to, when it could be told.
    """

    kind: str
    x: float
    y: float
    width: float
    height: float
    confidence: float
    character: str | None = None


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

    def detect_subjects(
        self, model: str, image_bytes: bytes, characters: list[str], **kwargs
    ) -> list[SubjectBox]:
        """Return normalized boxes for faces, hands, birds and key objects on a page."""
        ...
