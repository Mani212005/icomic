"""Store provider images and clean page derivatives, preserving raw sources."""

import logging
from dataclasses import replace

from ..adapters.image.port import ImageResult
from ..adapters.meta_http import detect_image_media_type
from ..adapters.storage.port import StoragePort
from .page_art import clean_page_art

logger = logging.getLogger(__name__)

IMAGE_EXTENSIONS = {
    "image/png": "png",
    "image/jpeg": "jpg",
    "image/webp": "webp",
    "image/gif": "gif",
}


def store_image(storage: StoragePort, key_stem: str, result: ImageResult) -> str:
    media_type = detect_image_media_type(result.image_bytes)
    if media_type not in IMAGE_EXTENSIONS:
        raise ValueError("Provider returned an unsupported image media type")
    return storage.put(f"{key_stem}.{IMAGE_EXTENSIONS[media_type]}", result.image_bytes, media_type)


def store_page_image(storage: StoragePort, key_stem: str, result: ImageResult) -> str:
    """Clean once at page generation; all consumers use this stored working copy."""
    cleanup = clean_page_art(result.image_bytes)
    logger.info("Page art cleanup: %s; removed_boxes=%s", cleanup.reason, cleanup.removed_boxes)
    if cleanup.removed_boxes:
        store_image(storage, f"{key_stem}_raw", result)
        result = replace(result, image_bytes=cleanup.image_bytes, media_type="image/png")
    return store_image(storage, key_stem, result)
