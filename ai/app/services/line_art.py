"""Final line art generation service."""

from typing import Any
from uuid import uuid4

from ..adapters.image.port import ImagePort
from ..adapters.storage.port import StoragePort
from ..gen.models import CharacterRenderIdentity, PanelTextItem
from .images import store_page_image
from .render_brief import build_page_render_request


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
        character_identities: list[CharacterRenderIdentity] | None = None,
    ) -> tuple[str, str, dict[str, Any]]:
        """Generate final clean closed-contour inking from approved draft."""
        brief, reference_images = build_page_render_request(
            page_number=page_number,
            panels=panels,
            style=style,
            storage=self.storage_adapter,
            character_identities=character_identities,
            character_reference_image_urls=character_reference_image_urls,
            draft_image_url=draft_image_url,
        )
        stage_prompt = (
            f"Comic book page {page_number} final black and white line art, {len(panels)}-panel grid. "
            "portrait-oriented full comic page, taller than wide, composed to fit the canvas without cropping. "
            "Bold clean closed-contour comic book lines, thick outlines, flat white fills, "
            "no shading, high contrast graphic novel inking. Follow the approved draft composition. "
        )
        prompt = f"{stage_prompt}\n{brief}"

        result = self.image_adapter.generate(
            prompt=prompt,
            reference_images=reference_images,
            hashed_user_id=hashed_user_id,
            style=style,
        )

        url = store_page_image(
            self.storage_adapter,
            f"pages/{page_id}/{uuid4().hex}/line_art_page_{page_number}",
            result,
        )
        return url, result.model_id, result.provenance_metadata
