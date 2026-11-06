"""Seeded, offline print-finishing experiments for painted comic art.

The function in this module works on pixels only. It has no knowledge of panels,
lettering, or page layout; an optional mask keeps pixels outside the painted art
untouched when a caller has that boundary available.
"""

from __future__ import annotations

from enum import Enum
from hashlib import blake2b
from io import BytesIO
from typing import Final

import numpy as np
from PIL import Image, ImageFilter


class FinishStrength(str, Enum):
    OFF = "off"
    LIGHT = "light"
    PRINT = "print"
    PLUS = "plus"
    HEAVY = "heavy"


class FinishTreatment(str, Enum):
    PAPER = "paper"
    HALFTONE = "halftone"
    REGISTRATION = "registration"
    MATTE = "matte"
    GRAIN = "grain"
    PRINT = "print"


_STRENGTH: Final = {
    FinishStrength.LIGHT: 0.48,
    FinishStrength.PRINT: 1.0,
    FinishStrength.PLUS: 1.75,
    FinishStrength.HEAVY: 2.5,
}
_PAPER_TINT: Final = np.array([248.0, 244.0, 232.0], dtype=np.float32)


def page_seed(stable_id: str) -> int:
    """Return a stable 64-bit seed for a page or artifact identifier."""
    digest = blake2b(stable_id.encode("utf-8"), digest_size=8, person=b"icomic-fn")
    return int.from_bytes(digest.digest(), "big")


def finish_image(
    image_bytes: bytes,
    *,
    seed: int | str,
    strength: FinishStrength | str = FinishStrength.PRINT,
    treatment: FinishTreatment | str = FinishTreatment.PRINT,
    art_mask: bytes | None = None,
) -> bytes:
    """Apply a deterministic finish to an encoded image and return encoded bytes.

    ``art_mask`` is an optional same-size encoded mask. White pixels receive the
    treatment and black pixels are preserved. Supported modes are L, RGB, and
    RGBA. The image format, dimensions, and mode are preserved. ``off`` returns
    the original bytes unchanged, including metadata and encoding details.
    """
    strength = FinishStrength(strength)
    treatment = FinishTreatment(treatment)
    if strength is FinishStrength.OFF:
        return image_bytes

    with Image.open(BytesIO(image_bytes)) as source:
        source.load()
        if source.mode not in ("L", "RGB", "RGBA"):
            raise ValueError(f"unsupported image mode: {source.mode}")
        original = source.copy()
        image_format = source.format or "PNG"

    mask: Image.Image | None = None
    if art_mask is not None:
        with Image.open(BytesIO(art_mask)) as mask_image:
            mask_image.load()
            if mask_image.size != original.size:
                raise ValueError("art_mask dimensions must match the image")
            mask = mask_image.convert("L")
            if mask.getextrema() == (0, 0):
                return image_bytes

    rgb = original.convert("RGB")
    array = np.asarray(rgb, dtype=np.float32)
    rng = np.random.default_rng(_seed_value(seed))
    result = _apply(array, rng, strength, treatment)
    finished = Image.fromarray(np.clip(np.rint(result), 0, 255).astype(np.uint8), "RGB")

    if mask is not None:
        finished = Image.composite(finished, rgb, mask)
    if original.mode == "L":
        finished = finished.convert("L")
    elif original.mode == "RGBA":
        finished.putalpha(original.getchannel("A"))

    output = BytesIO()
    save_options: dict[str, object] = {}
    if image_format.upper() == "JPEG":
        save_options = {"quality": 95, "subsampling": 0}
    finished.save(output, format=image_format, **save_options)
    return output.getvalue()


def _seed_value(seed: int | str) -> int:
    if isinstance(seed, int):
        return seed % (1 << 64)
    return page_seed(seed)


def _apply(
    source: np.ndarray,
    rng: np.random.Generator,
    strength: FinishStrength,
    treatment: FinishTreatment,
) -> np.ndarray:
    amount = _STRENGTH[strength]
    image = source.copy()
    if treatment in (FinishTreatment.PAPER, FinishTreatment.PRINT):
        paper_strength = {
            FinishStrength.LIGHT: 0.80,
            FinishStrength.PRINT: 0.80,
            FinishStrength.PLUS: 1.08,
            FinishStrength.HEAVY: 1.28,
        }[strength]
        image = _paper(
            image, rng, amount * (paper_strength if treatment is FinishTreatment.PRINT else 1.0)
        )
    if treatment in (FinishTreatment.HALFTONE, FinishTreatment.PRINT):
        halftone_strength = {
            FinishStrength.LIGHT: 0.38,
            FinishStrength.PRINT: 0.38,
            FinishStrength.PLUS: 0.80,
            FinishStrength.HEAVY: 1.12,
        }[strength]
        image = _halftone(
            image, amount * (halftone_strength if treatment is FinishTreatment.PRINT else 1.0)
        )
    if treatment in (FinishTreatment.REGISTRATION, FinishTreatment.PRINT):
        registration_strength = {
            FinishStrength.LIGHT: 0.10,
            FinishStrength.PRINT: 0.10,
            FinishStrength.PLUS: 0.34,
            FinishStrength.HEAVY: 0.72,
        }[strength]
        image = _registration_and_bleed(
            image,
            rng,
            amount * (registration_strength if treatment is FinishTreatment.PRINT else 1.0),
        )
    if treatment in (FinishTreatment.MATTE, FinishTreatment.PRINT):
        matte_strength = {
            FinishStrength.LIGHT: 0.58,
            FinishStrength.PRINT: 0.58,
            FinishStrength.PLUS: 0.82,
            FinishStrength.HEAVY: 1.0,
        }[strength]
        image = _matte(
            image,
            amount * (matte_strength if treatment is FinishTreatment.PRINT else 1.0),
        )
    if treatment in (FinishTreatment.GRAIN, FinishTreatment.PRINT):
        grain_strength = {
            FinishStrength.LIGHT: 0.42,
            FinishStrength.PRINT: 0.42,
            FinishStrength.PLUS: 0.78,
            FinishStrength.HEAVY: 1.1,
        }[strength]
        image = _grain(
            image, rng, amount * (grain_strength if treatment is FinishTreatment.PRINT else 1.0)
        )
    if treatment in (FinishTreatment.MATTE, FinishTreatment.PRINT):
        image = np.minimum(image, 254.0)
    return image


def _noise_field(shape: tuple[int, int], rng: np.random.Generator, scale: int) -> np.ndarray:
    height, width = shape
    small = rng.normal(0.0, 1.0, (max(2, height // scale), max(2, width // scale)))
    small_image = Image.fromarray(np.uint8(np.clip(small * 32 + 128, 0, 255)), "L")
    enlarged = small_image.resize((width, height), Image.Resampling.BICUBIC)
    return (np.asarray(enlarged, dtype=np.float32) - 128.0) / 32.0


def _paper(image: np.ndarray, rng: np.random.Generator, amount: float) -> np.ndarray:
    height, width, _ = image.shape
    coarse = _noise_field((height, width), rng, max(16, min(width, height) // 54))
    tooth = _noise_field((height, width), rng, max(3, min(width, height) // 480))
    fibre_small = rng.normal(
        0.0,
        1.0,
        (max(2, height // 28), max(2, width // 240)),
    )
    fibre_image = Image.fromarray(np.uint8(np.clip(fibre_small * 28 + 128, 0, 255)), "L")
    fibre_image = fibre_image.resize((width, height), Image.Resampling.BICUBIC)
    fibre_image = fibre_image.filter(ImageFilter.GaussianBlur(max(0.25, width / 2600)))
    fibres = (np.asarray(fibre_image, dtype=np.float32) - 128.0) / 28.0
    luma = image @ np.array([0.2126, 0.7152, 0.0722], dtype=np.float32)
    light_weight = np.clip((luma - 24.0) / 180.0, 0.0, 1.0)
    dark_weight = 0.24 + 0.76 * light_weight
    texture = coarse * 0.45 + tooth * 0.32 + fibres * 0.23
    factor = 1.0 + texture * (0.032 * amount) * dark_weight
    return image * factor[..., None]


def _halftone(image: np.ndarray, amount: float) -> np.ndarray:
    height, width, _ = image.shape
    luma = image @ np.array([0.2126, 0.7152, 0.0722], dtype=np.float32)
    gray = Image.fromarray(np.uint8(np.clip(luma, 0, 255)), "L")
    edge = np.asarray(gray.filter(ImageFilter.FIND_EDGES), dtype=np.float32)
    smooth = np.asarray(gray.filter(ImageFilter.GaussianBlur(1.2)), dtype=np.float32)
    yy, xx = np.mgrid[:height, :width]
    angle = np.deg2rad(23.0)
    period = max(5.0, min(width, height) / 180.0)
    phase = ((xx * np.cos(angle) + yy * np.sin(angle)) / period) % 1.0
    dots = np.cos(phase * 2.0 * np.pi)
    shadow = np.clip((168.0 - smooth) / 100.0, 0.0, 1.0)
    flat = np.clip((13.0 - edge) / 13.0, 0.0, 1.0)
    mask = shadow * flat
    modulation = 1.0 + dots * 0.035 * amount * mask
    return image * modulation[..., None]


def _registration_and_bleed(
    image: np.ndarray, rng: np.random.Generator, amount: float
) -> np.ndarray:
    height, width, _ = image.shape
    # Displacements are specified at the 1280px working width and scale with it.
    scale = width / 1280.0
    max_shift = min(0.95 * scale, 0.72 * scale * amount)
    angle = rng.uniform(0.0, 2.0 * np.pi)
    shifts = [
        (0.0, 0.0),
        (np.cos(angle) * max_shift, np.sin(angle) * max_shift),
        (-np.sin(angle) * max_shift * 0.65, np.cos(angle) * max_shift * 0.65),
    ]
    channels = []
    for index, (dx, dy) in enumerate(shifts):
        pixels = np.uint8(np.clip(image[:, :, index], 0, 255))
        padded = np.pad(pixels, ((2, 2), (2, 2)), mode="edge")
        channel = Image.fromarray(padded, "L")
        if dx or dy:
            channel = channel.transform(
                (width + 4, height + 4),
                Image.Transform.AFFINE,
                (1, 0, -dx, 0, 1, -dy),
                resample=Image.Resampling.BILINEAR,
                fillcolor=0,
            )
        channels.append(np.asarray(channel.crop((2, 2, width + 2, height + 2)), dtype=np.float32))
    shifted = np.stack(channels, axis=2)
    # Very restrained ink spread along dark contours, reduced on broad flat regions.
    gray = Image.fromarray(np.uint8(np.clip(shifted.mean(axis=2), 0, 255)), "L")
    gray_values = np.asarray(gray, dtype=np.float32)
    eroded = np.asarray(gray.filter(ImageFilter.MinFilter(3)), dtype=np.float32)
    dark_ink = np.clip((150.0 - gray_values) / 130.0, 0.0, 1.0)
    edge_spread = np.minimum(shifted, eroded[..., None] * 1.02)
    return shifted * (1.0 - 0.035 * amount * dark_ink[..., None]) + edge_spread * (
        0.035 * amount * dark_ink[..., None]
    )


def _matte(image: np.ndarray, amount: float) -> np.ndarray:
    amount = min(2.0, max(0.0, amount))
    blend = min(1.0, amount)
    luma = image @ np.array([0.2126, 0.7152, 0.0722], dtype=np.float32)
    contrast = (image - 127.5) * 1.035 + 127.5
    # A restrained S curve and lifted shadows; paper tint is strongest in highlights.
    normalized = np.clip((contrast - 128.0) / 128.0, -1.0, 1.0)
    curved = 128.0 + normalized * (1.0 + 0.12 * (1.0 - normalized**2)) * 128.0
    lifted = curved * 0.982 + 4.2 * amount
    saturation = 1.0 - 0.055 * amount
    gray = luma[..., None]
    matte = gray + (lifted - gray) * saturation
    highlight = np.clip((luma - 155.0) / 100.0, 0.0, 1.0)[..., None]
    matte = matte * (1.0 - 0.026 * amount * highlight) + _PAPER_TINT * (0.026 * amount * highlight)
    return image * (1.0 - blend) + matte * blend


def _grain(image: np.ndarray, rng: np.random.Generator, amount: float) -> np.ndarray:
    luma = image @ np.array([0.2126, 0.7152, 0.0722], dtype=np.float32)
    weight = np.clip(0.38 + luma / 255.0 * 0.62, 0.38, 1.0)
    noise = rng.normal(0.0, 2.0 * amount, luma.shape) * weight
    return image + noise[..., None]
