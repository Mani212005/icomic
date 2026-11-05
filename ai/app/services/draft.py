"""Draft sketch generation service (one rough image per page)."""

from typing import Any
from uuid import uuid4

from ..adapters.image.port import ImagePort
from ..adapters.storage.port import StoragePort
from ..gen.models import CharacterRenderIdentity, PanelTextItem
from .image_prompts import DRAFT_PROMPT_VERSION
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
        from .style_bible import resolve_style_bible

        style_bible = resolve_style_bible(style)
        brief, reference_images = build_page_render_request(
            page_number=page_number,
            panels=panels,
            style=style_bible.style_id,
            storage=self.storage_adapter,
            character_identities=character_identities,
            character_reference_image_urls=character_reference_image_urls,
        )
        stage_prompt = style_bible.format_for_draft(
            page_number=page_number, panel_count=len(panels)
        )
        prompt = f"{stage_prompt}\n{brief}"

        result = self.image_adapter.generate(
            prompt=prompt,
            reference_images=reference_images,
            hashed_user_id=hashed_user_id,
            style=style_bible.style_id,
            role="draft",
            prompt_version=DRAFT_PROMPT_VERSION,
        )
        result.provenance_metadata["role"] = "draft"
        result.provenance_metadata["prompt_version"] = DRAFT_PROMPT_VERSION

        url = store_page_image(
            self.storage_adapter, f"pages/{page_id}/{uuid4().hex}/draft_page_{page_number}", result
        )
        return url, result.model_id, result.provenance_metadata
