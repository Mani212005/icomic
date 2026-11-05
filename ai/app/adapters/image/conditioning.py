"""Bounded, derived conditioning images. Stored master bytes are never changed."""

import hashlib
import io
import os
from dataclasses import dataclass

from PIL import Image, ImageOps, UnidentifiedImageError


class ImagePayloadError(ValueError):
    """An input image or complete request cannot fit the configured wire budget."""

    code = "IMAGE_PAYLOAD_LIMIT"


@dataclass(frozen=True)
class ConditioningLimits:
    max_side: int = 1024
    quality: int = 85
    image_bytes: int = 262144
    total_bytes: int = 2097152
    request_bytes: int = 3145728

    @classmethod
    def from_env(cls):
        values = {
            "max_side": ("META_IMAGE_REFERENCE_MAX_SIDE", 1024),
            "quality": ("META_IMAGE_REFERENCE_WEBP_QUALITY", 85),
            "image_bytes": ("META_IMAGE_MAX_REFERENCE_BYTES", 262144),
            "total_bytes": ("META_IMAGE_MAX_TOTAL_REFERENCE_BYTES", 2097152),
            "request_bytes": ("META_IMAGE_MAX_REQUEST_BYTES", 3145728),
        }
        try:
            result = cls(
                **{field: int(os.getenv(env, default)) for field, (env, default) in values.items()}
            )
        except ValueError as exc:
            raise ImagePayloadError("Image conditioning limits must be positive integers") from exc
        if min(result.max_side, result.image_bytes, result.total_bytes, result.request_bytes) <= 0:
            raise ImagePayloadError("Image conditioning limits must be positive")
        if not 1 <= result.quality <= 100:
            raise ImagePayloadError("Image conditioning WebP quality must be in 1..100")
        return result


def describe_image(content: bytes) -> dict:
    try:
        with Image.open(io.BytesIO(content)) as image:
            width, height = image.size
    except (UnidentifiedImageError, OSError, ValueError) as exc:
        raise ImagePayloadError("Conditioning input is not a decodable image") from exc
    return {
        "sha256": hashlib.sha256(content).hexdigest(),
        "width": width,
        "height": height,
        "bytes": len(content),
    }


def conditioning_copy(content: bytes, limits: ConditioningLimits) -> tuple[bytes, dict]:
    try:
        with Image.open(io.BytesIO(content)) as source:
            source.load()
            if (
                source.format == "WEBP"
                and max(source.size) <= limits.max_side
                and len(content) <= limits.image_bytes
                and source.getexif().get(274, 1) == 1
            ):
                return content, {
                    **describe_image(content),
                    "source_sha256": hashlib.sha256(content).hexdigest(),
                }
            image = ImageOps.exif_transpose(source).convert("RGBA")
    except (UnidentifiedImageError, OSError, ValueError) as exc:
        raise ImagePayloadError("Conditioning input is not a decodable image") from exc
    image.thumbnail((limits.max_side, limits.max_side), Image.Resampling.LANCZOS)
    while True:
        output = io.BytesIO()
        image.save(output, format="WEBP", quality=limits.quality, method=6)
        derived = output.getvalue()
        if len(derived) <= limits.image_bytes:
            return derived, {
                **describe_image(derived),
                "source_sha256": hashlib.sha256(content).hexdigest(),
            }
        if max(image.size) <= 64:
            raise ImagePayloadError(
                f"Conditioning image exceeds {limits.image_bytes} byte limit at minimum resolution"
            )
        side = max(64, int(max(image.size) * 0.75))
        image.thumbnail((side, side), Image.Resampling.LANCZOS)


def check_image_budget(images: list[bytes], limits: ConditioningLimits) -> None:
    if any(len(image) > limits.image_bytes for image in images):
        raise ImagePayloadError(f"Conditioning image exceeds {limits.image_bytes} byte limit")
    total = sum(map(len, images))
    if total > limits.total_bytes:
        raise ImagePayloadError(
            f"Attached images total {total} bytes exceeds {limits.total_bytes} byte limit"
        )
