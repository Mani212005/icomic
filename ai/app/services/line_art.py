"""Final line art generation service."""

from typing import Any

from ..adapters.image.port import ImagePort
from ..adapters.storage.port import StoragePort
from ..gen.models import PanelTextItem


class LineArtService:
    def __init__(self, image_adapter: ImagePort, storage_adapter: StoragePort):
        self.image_adapter = image_adapter
        self.storage_adapter = storage_adapter

    def generate_line_art(
        self,
        page_id: str,
        page_number: int,
        draft_image_url: str,
        panels: list[PanelTextItem],
        style: str,
        hashed_user_id: str,
        character_reference_image_urls: list[str] | None = None,
    ) -> tuple[str, str, dict[str, Any]]:
        prompt = (
            f"Comic book page {page_number} final black and white line art. "
            f"Bold clean closed-contour comic book lines, thick black outlines, "
            f"flat white fills, no shading, high contrast graphic novel inking. Style: {style}"
        )

        result = self.image_adapter.generate(
            prompt=prompt,
            reference_images=[],
            hashed_user_id=hashed_user_id,
            style=style,
        )

        key = f"pages/{page_id}/line_art_page_{page_number}.png"
        url = self.storage_adapter.put(key, result.image_bytes, "image/png")
        return url, result.model_id, result.provenance_metadata
