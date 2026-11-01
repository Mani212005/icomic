"""Meta image adapter implementation (deferred until API credits are available)."""

import os

from .port import ImagePort, ImageResult


class MetaImageAdapter(ImagePort):
    def __init__(self, api_key: str | None = None, base_url: str = "https://api.meta.ai"):
        self.api_key = api_key or os.getenv("META_API_KEY")
        self.base_url = base_url
        if not self.api_key:
            raise RuntimeError(
                "MetaImageAdapter requires META_API_KEY. Use FakeImageAdapter for local development and testing."
            )

    def generate(
        self, prompt: str, reference_images: list[bytes], hashed_user_id: str, style: str
    ) -> ImageResult:
        raise NotImplementedError(
            "Live Meta muse-image-1.0 integration is deferred until API credits are provisioned."
        )
