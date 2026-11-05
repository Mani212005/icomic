"""Draft sketch generation service (one rough image per page)."""

from typing import Any
from uuid import uuid4

from ..adapters.image.port import ImagePort
from ..adapters.storage.port import StoragePort
from ..gen.models import CharacterRenderIdentity, PanelTextItem
from .images import store_page_image
from .render_brief import build_page_render_request


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
        character_identities: list[CharacterRenderIdentity] | None = None,
    ) -> tuple[str, str, dict[str, Any]]:
        """Generate one rough draft sketch per page for composition review."""
        brief, reference_images = build_page_render_request(
            page_number=page_number,
            panels=panels,
            style=style,
            storage=self.storage_adapter,
            character_identities=character_identities,
            character_reference_image_urls=character_reference_image_urls,
        )
        stage_prompt = (
            f"Rough comic book page layout draft sketch, {len(panels)}-panel grid composition, "
            f"rough pencil layout for page {page_number}. Style: {style}. "
        )
        prompt = f"{stage_prompt}\n{brief}"

        result = self.image_adapter.generate(
            prompt=prompt,
            reference_images=reference_images,
            hashed_user_id=hashed_user_id,
            style=style,
        )

        url = store_page_image(
            self.storage_adapter, f"pages/{page_id}/{uuid4().hex}/draft_page_{page_number}", result
        )
        return url, result.model_id, result.provenance_metadata
