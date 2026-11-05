"""Small, valid images for tests that exercise conditioning and decoding."""

import hashlib
import io

from PIL import Image


def image_bytes(label: str, format: str = "WEBP") -> bytes:
    output = io.BytesIO()
    colour = tuple(hashlib.sha256(label.encode()).digest()[:3])
    Image.new("RGB", (64, 64), colour).save(output, format=format)
    return output.getvalue()
