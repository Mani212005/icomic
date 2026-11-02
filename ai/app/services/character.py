"""Character design and reference image generation service."""

from typing import Any

from ..adapters.image.port import ImagePort
from ..adapters.storage.port import StoragePort


class CharacterService:
    """Service managing character reference image generation and storage."""

    def __init__(self, image_adapter: ImagePort, storage_adapter: StoragePort):
        self.image_adapter = image_adapter
        self.storage_adapter = storage_adapter

    def generate_reference_image(
        self,
        character_id: str,
        character_name: str,
        prompt: str,
        hashed_user_id: str,
        style: str,
        reference_images: list[bytes] | None = None,
    ) -> tuple[str, str, dict[str, Any]]:
        """Generate a character reference image and store it in object storage."""
        full_prompt = (
            f"Character reference sheet for {character_name}: {prompt}. "
            f"Full turnaround, clear facial details, front and side view. Style: {style}"
        )
        result = self.image_adapter.generate(
            prompt=full_prompt,
            reference_images=reference_images or [],
            hashed_user_id=hashed_user_id,
            style=style,
        )

        key = f"characters/{character_id}/reference.png"
        url = self.storage_adapter.put(key, result.image_bytes, "image/png")
        return url, result.model_id, result.provenance_metadata
