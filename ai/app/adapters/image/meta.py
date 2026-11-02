"""Meta image adapter inert stub (deferred until API credits are available)."""

from .port import ImagePort, ImageResult


class MetaImageAdapter(ImagePort):
    """Inert stub for Meta image models (muse-image-1.0).

    Raises a clear not-configured error upon initialization.
    No live API calls or API keys are read.
    """

    def __init__(self, *args, **kwargs):
        raise RuntimeError(
            "MetaImageAdapter is not configured: live Meta muse-image-1.0 integration is deferred "
            "until API credits are provisioned. Use FakeImageAdapter (default) via ADAPTER_IMAGE=fake."
        )

    def generate(
        self, prompt: str, reference_images: list[bytes], hashed_user_id: str, style: str
    ) -> ImageResult:
        raise NotImplementedError("MetaImageAdapter is inert and does not perform live calls.")
