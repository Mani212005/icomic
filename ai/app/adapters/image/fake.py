"""Deterministic fake image adapter returning a test PNG without network."""

import hashlib
import struct
import zlib
from typing import Any

from .port import ImagePort, ImageResult


def _generate_test_png(width: int = 64, height: int = 64) -> bytes:
    raw_data = bytearray()
    for y in range(height):
        raw_data.append(0)  # filter type None
        for x in range(width):
            if x == 0 or x == width - 1 or y == 0 or y == height - 1:
                raw_data.extend((0, 0, 0))  # black border
            else:
                raw_data.extend((255, 255, 255))  # white background

    def chunk(tag: bytes, data: bytes) -> bytes:
        return (
            struct.pack(">I", len(data))
            + tag
            + data
            + struct.pack(">I", zlib.crc32(tag + data) & 0xFFFFFFFF)
        )

    ihdr = struct.pack(">IIBBBBB", width, height, 8, 2, 0, 0, 0)
    return (
        b"\x89PNG\r\n\x1a\n"
        + chunk(b"IHDR", ihdr)
        + chunk(b"IDAT", zlib.compress(bytes(raw_data)))
        + chunk(b"IEND", b"")
    )


class FakeImageAdapter(ImagePort):
    """Deterministic fake image generator returning fixed test PNG and C2PA provenance."""

    def __init__(self, model_id: str = "muse-image-1.0-fake"):
        self.model_id = model_id
        self._png_bytes = _generate_test_png()
        self.recorded_calls: list[dict[str, Any]] = []
        self.calls: list[dict[str, Any]] = self.recorded_calls
        self.recorded_inputs: list[dict[str, Any]] = self.recorded_calls

    def generate(
        self, prompt: str, reference_images: list[bytes], hashed_user_id: str, style: str
    ) -> ImageResult:
        self.recorded_calls.append(
            {
                "prompt": prompt,
                "reference_images": list(reference_images),
                "hashed_user_id": hashed_user_id,
                "style": style,
            }
        )
        h = hashlib.sha256(f"{prompt}:{hashed_user_id}:{style}".encode()).hexdigest()
        request_id = f"req-fake-{h[:12]}"
        provenance = {
            "c2pa": {
                "claim_generator": "Meta-AI-Provenance/1.0",
                "signature": f"sig-fake-{h[:16]}",
                "watermark_present": True,
            },
            "model": self.model_id,
            "style": style,
        }
        return ImageResult(
            image_bytes=self._png_bytes,
            provenance_metadata=provenance,
            model_id=self.model_id,
            request_id=request_id,
            media_type="image/png",
        )
