"""Image port protocol for image generation."""

from dataclasses import dataclass
from typing import Any, Protocol


@dataclass
class ImageResult:
    image_bytes: bytes
    provenance_metadata: dict[str, Any]
    model_id: str
    request_id: str


class ImagePort(Protocol):
    def generate(
        self, prompt: str, reference_images: list[bytes], hashed_user_id: str, style: str
    ) -> ImageResult:
        """Generate an image from prompt, optional reference images, and style."""
        ...
