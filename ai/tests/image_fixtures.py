"""Small, valid images for tests that exercise conditioning and decoding."""

import hashlib
import io

from PIL import Image


def image_bytes(label: str, format: str = "WEBP") -> bytes:
    output = io.BytesIO()
    colour = tuple(hashlib.sha256(label.encode()).digest()[:3])
    Image.new("RGB", (64, 64), colour).save(output, format=format)
    return output.getvalue()


def placeholder_png(width: int = 64, height: int = 64) -> bytes:
    """A bordered PNG for tests; the product export path never substitutes art."""
    image = Image.new("RGB", (width, height), (240, 240, 245))
    image.paste((40, 40, 40), (0, 0, width, height))
    image.paste((240, 240, 245), (1, 1, width - 1, height - 1))
    output = io.BytesIO()
    image.save(output, format="PNG")
    return output.getvalue()
