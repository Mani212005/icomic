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

    def __init__(
        self, model_id: str = "muse-image-1.0-fake", replay_adapter: ImagePort | None = None
    ):
        self.model_id = model_id
        self.replay_adapter = replay_adapter
        self._png_bytes = _generate_test_png()
        self.recorded_calls: list[dict[str, Any]] = []
        self.calls: list[dict[str, Any]] = self.recorded_calls
        self.recorded_inputs: list[dict[str, Any]] = self.recorded_calls

    def generate(
        self,
        prompt: str,
        reference_images: list[bytes],
        hashed_user_id: str,
        style: str,
        role: str = "image",
        prompt_version: str = "1.0.0",
        **kwargs: Any,
    ) -> ImageResult:
        if kwargs:
            unsupported = sorted(kwargs.keys())
            raise ValueError(
                f"Unsupported image options: {', '.join(unsupported)}. "
                "Image generation does not support size, quality, seed, or batch count parameters."
            )
        self.recorded_calls.append(
            {
                "prompt": prompt,
                "reference_images": list(reference_images),
                "hashed_user_id": hashed_user_id,
                "style": style,
                "role": role,
                "prompt_version": prompt_version,
            }
        )
        if self.replay_adapter is not None:
            return self.replay_adapter.generate(
                prompt,
                reference_images,
                hashed_user_id,
                style,
                role=role,
                prompt_version=prompt_version,
                **kwargs,
            )
        h = hashlib.sha256(f"{prompt}:{hashed_user_id}:{style}".encode()).hexdigest()
        request_id = f"req-fake-{h[:12]}"
        overlay = '"lettering_mode": "overlay"' in prompt
        width, height = (256, 384) if overlay else (64, 64)
        # Portrait, text-free stand-in for overlay requests. No glyph/bubble drawing.
        png_bytes = _generate_test_png(width, height) if overlay else self._png_bytes
        source_hash = hashlib.sha256(png_bytes).hexdigest()
        provenance = {
            "c2pa": {
                "claim_generator": "Meta-AI-Provenance/1.0",
                "signature": f"sig-fake-{h[:16]}",
                "watermark_present": True,
                "manifest_marker_detected": False,
            },
            "model": self.model_id,
            "style": style,
            "cost_usd": 0.01,
            "media_type": "image/png",
            "source_hash": source_hash,
            "dimensions": {"width": width, "height": height},
            "width": width,
            "height": height,
            "role": role,
            "prompt_version": prompt_version,
            "response_keys": ["created", "id", "model", "object", "output", "provenance"],
        }
        return ImageResult(
            image_bytes=png_bytes,
            provenance_metadata=provenance,
            model_id=self.model_id,
            request_id=request_id,
            media_type="image/png",
            width=width,
            height=height,
        )
