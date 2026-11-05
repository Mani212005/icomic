"""Finished painted comic book page generation service (opt-in prototype route)."""

from typing import Any
from uuid import uuid4

from ..adapters.image.port import ImagePort
from ..adapters.storage.port import StoragePort
from ..gen.models import CharacterRenderIdentity, PanelTextItem
from .images import store_page_image
from .render_brief import build_page_render_request

VARIANT_DRAFT_CONDITIONED = "draft-conditioned"
VARIANT_DIRECT_FROM_BRIEF = "direct-from-brief"


class PaintedPageService:
    """Service generating finished painted comic pages (opt-in prototype)."""

    def __init__(self, image_adapter: ImagePort, storage_adapter: StoragePort):
        self.image_adapter = image_adapter
        self.storage_adapter = storage_adapter

    def generate_painted_page(
        self,
        page_id: str,
        page_number: int,
        panels: list[PanelTextItem],
        style: str,
        hashed_user_id: str,
        variant: str,
        draft_image_url: str | None = None,
        character_reference_image_urls: list[str] | None = None,
        character_identities: list[CharacterRenderIdentity] | None = None,
        style_reference_image_url: str | None = None,
    ) -> tuple[str, str, dict[str, Any], str]:
        """Generate finished painted page under chosen variant (composition-conditioned or direct)."""
        chosen_variant = variant
        if chosen_variant not in (VARIANT_DRAFT_CONDITIONED, VARIANT_DIRECT_FROM_BRIEF):
            raise ValueError(f"Unsupported painted page variant: {chosen_variant}")

        effective_draft_url = (
            draft_image_url if chosen_variant == VARIANT_DRAFT_CONDITIONED else None
        )

        brief, reference_images = build_page_render_request(
            page_number=page_number,
            panels=panels,
            style=style,
            storage=self.storage_adapter,
            character_identities=character_identities,
            character_reference_image_urls=character_reference_image_urls,
            draft_image_url=effective_draft_url,
            style_reference_image_url=style_reference_image_url,
        )

        if chosen_variant == VARIANT_DRAFT_CONDITIONED:
            stage_prompt = (
                f"Finished painted comic book page {page_number}, {len(panels)}-panel grid composition. "
                "Create one upright portrait page with a 2:3 aspect ratio (height is 1.5 times width). "
                "Fully rendered painterly artwork with rich colors, nuanced tonal modeling, directional lighting, "
                "atmospheric depth, expressive character detail, believable cloth folds and material textures. "
                "Follow the approved draft composition for geometry and staging while delivering a complete, "
                "polished, full-color painted finish. "
            )
        else:
            stage_prompt = (
                f"Finished painted comic book page {page_number}, {len(panels)}-panel grid composition directly from brief. "
                "Create one upright portrait page with a 2:3 aspect ratio (height is 1.5 times width); never use landscape orientation. "
                "Fully rendered painterly artwork with rich colors, nuanced tonal modeling, directional lighting, "
                "atmospheric depth, expressive character detail, believable cloth folds and material textures. "
                "Keep each character in the location and physical situation described for that panel; do not place a character in the sea or another hazardous setting unless the action explicitly says so. "
                "Establish complete panel composition, geometry and staging directly from the ordered brief while "
                "delivering a polished, full-color painted finish. "
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
            f"pages/{page_id}/{uuid4().hex}/painted_page_{page_number}",
            result,
        )
        return url, result.model_id, result.provenance_metadata, chosen_variant
