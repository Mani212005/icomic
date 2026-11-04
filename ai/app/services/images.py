"""Store provider images without changing their bytes or provenance."""

from ..adapters.image.port import ImageResult
from ..adapters.meta_http import detect_image_media_type
from ..adapters.storage.port import StoragePort

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
