"""Final line art generation service."""

from typing import Any

from ..adapters.image.port import ImagePort
from ..adapters.storage.port import StoragePort
from ..gen.models import PanelTextItem
from .image_prompts import IMAGE_ONLY_INSTRUCTIONS
from .images import store_page_image


class LineArtService:
    """Service generating final black-and-white line art per comic page."""

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
        """Generate final clean closed-contour inking from approved draft."""
        panel_count = len(panels)
        prompt = (
            f"Comic book page {page_number} final black and white line art, {panel_count}-panel grid. "
            f"Bold clean closed-contour comic book lines, thick outlines, flat white fills, "
            f"no shading, high contrast graphic novel inking based on draft {draft_image_url}. Style: {style}. "
            f"{IMAGE_ONLY_INSTRUCTIONS}"
        )
        if character_reference_image_urls:
            refs = ", ".join(character_reference_image_urls)
            prompt += f". Character references: {refs}"

        reference_images: list[bytes] = [self.storage_adapter.get(draft_image_url)]
        if character_reference_image_urls:
            for ref_url in character_reference_image_urls:
                reference_images.append(self.storage_adapter.get(ref_url))

        result = self.image_adapter.generate(
            prompt=prompt,
            reference_images=reference_images,
            hashed_user_id=hashed_user_id,
            style=style,
        )

        url = store_page_image(
            self.storage_adapter, f"pages/{page_id}/line_art_page_{page_number}", result
        )
        return url, result.model_id, result.provenance_metadata
