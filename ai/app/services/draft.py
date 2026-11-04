"""Draft sketch generation service (one rough image per page)."""

from typing import Any

from ..adapters.image.port import ImagePort
from ..adapters.storage.port import StoragePort
from ..gen.models import PanelTextItem
from .image_prompts import IMAGE_ONLY_INSTRUCTIONS
from .images import store_image


class DraftService:
    """Service generating rough draft sketch layouts for full comic pages."""

    def __init__(self, image_adapter: ImagePort, storage_adapter: StoragePort):
        self.image_adapter = image_adapter
        self.storage_adapter = storage_adapter

    def generate_draft_sketch(
        self,
        page_id: str,
        page_number: int,
        panels: list[PanelTextItem],
        style: str,
        hashed_user_id: str,
        character_reference_image_urls: list[str] | None = None,
    ) -> tuple[str, str, dict[str, Any]]:
        """Generate one rough draft sketch per page for composition review."""
        panel_count = len(panels)
        prompt = (
            f"Rough comic book page layout draft sketch, {panel_count}-panel grid composition, "
            f"loose stick-figure and rough pencil layout for page {page_number}. Style: {style}. "
            f"{IMAGE_ONLY_INSTRUCTIONS}"
        )
        if character_reference_image_urls:
            refs = ", ".join(character_reference_image_urls)
            prompt += f". Character references: {refs}"

        reference_images: list[bytes] = []
        if character_reference_image_urls:
            for ref_url in character_reference_image_urls:
                reference_images.append(self.storage_adapter.get(ref_url))

        result = self.image_adapter.generate(
            prompt=prompt,
            reference_images=reference_images,
            hashed_user_id=hashed_user_id,
            style=style,
        )

        url = store_image(self.storage_adapter, f"pages/{page_id}/draft_page_{page_number}", result)
        return url, result.model_id, result.provenance_metadata
