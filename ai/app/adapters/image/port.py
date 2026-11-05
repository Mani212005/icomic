"""Image port protocol for image generation."""

from dataclasses import dataclass
from typing import Any, Protocol, runtime_checkable


@dataclass
class ImageResult:
    image_bytes: bytes
    provenance_metadata: dict[str, Any]
    model_id: str
    request_id: str
    media_type: str | None = None
    width: int | None = None
    height: int | None = None


@runtime_checkable
class ImagePort(Protocol):
    """Protocol defining image generation interface."""

    def generate(
        self,
        prompt: str,
        reference_images: list[bytes],
        hashed_user_id: str,
        style: str,
        role: str = "image",
        prompt_version: str = "1.0.0",
        **kwargs: Any,
    ) -> ImageResult:
        """Generate an image from prompt, optional reference images, style, and metadata."""
        ...
