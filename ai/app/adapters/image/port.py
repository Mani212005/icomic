"""Image port protocol for image generation."""

from dataclasses import dataclass
from typing import Any, Protocol, runtime_checkable


@dataclass
class ImageResult:
    image_bytes: bytes
    provenance_metadata: dict[str, Any]
    model_id: str
    request_id: str


@runtime_checkable
class ImagePort(Protocol):
    """Protocol defining image generation interface."""

    def generate(
        self, prompt: str, reference_images: list[bytes], hashed_user_id: str, style: str
    ) -> ImageResult:
        """Generate an image from prompt, optional reference images, and style."""
        ...
