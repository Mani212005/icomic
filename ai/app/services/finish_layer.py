"""Seeded, offline print-finishing experiments for painted comic art.

The function in this module works on pixels only. It has no knowledge of panels,
lettering, or page layout; an optional mask keeps pixels outside the painted art
untouched when a caller has that boundary available.

Every treatment is a per-pixel pass over a (rows, width, 3) strip, except for the few
neighbourhood fields (blurs, edges, an erosion) that are built once for the whole page.
Strips of ``_STRIP_ROWS`` rows keep the temporaries cache resident, so peak memory stays
close to the page itself and the arithmetic is the same operation sequence, in the same
dtypes, as a whole-array evaluation: output bytes do not depend on the strip size.
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
_LUMA: Final = np.array([0.2126, 0.7152, 0.0722], dtype=np.float32)
_STRIP_ROWS: Final = 16


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
    finished = finish_pixels(
        image_bytes, seed=seed, strength=strength, treatment=treatment, art_mask=art_mask
    )
    if finished is None:
        return image_bytes
    return encode_finished(*finished)


def encode_finished(image: Image.Image, image_format: str) -> bytes:
    """Encode pixels from ``finish_pixels`` in the source format."""
    output = BytesIO()
    save_options: dict[str, object] = {}
    if image_format.upper() == "JPEG":
        save_options = {"quality": 95, "subsampling": 0}
    image.save(output, format=image_format, **save_options)
    return output.getvalue()


def finish_pixels(
    image_bytes: bytes,
    *,
    seed: int | str,
    strength: FinishStrength | str = FinishStrength.PRINT,
    treatment: FinishTreatment | str = FinishTreatment.PRINT,
    art_mask: bytes | None = None,
) -> tuple[Image.Image, str] | None:
    """Return the finished pixels and the source format, or ``None`` when nothing changes.

    This is ``finish_image`` before encoding, for callers that re-encode the result
    anyway (for example into a PDF page) and would otherwise pay for a lossless round trip.
    """
    strength = FinishStrength(strength)
    treatment = FinishTreatment(treatment)
    if strength is FinishStrength.OFF:
        return None

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
                return None

    rgb = original.convert("RGB")
    array = np.asarray(rgb, dtype=np.float32)
    rng = np.random.default_rng(_seed_value(seed))
    finished = Image.fromarray(_apply(array, rng, strength, treatment), "RGB")

    if mask is not None:
        finished = Image.composite(finished, rgb, mask)
    if original.mode == "L":
        finished = finished.convert("L")
    elif original.mode == "RGBA":
        finished.putalpha(original.getchannel("A"))
    return finished, image_format


def _seed_value(seed: int | str) -> int:
    if isinstance(seed, int):
        return seed % (1 << 64)
    return page_seed(seed)


def _strips(height: int):
    for start in range(0, height, _STRIP_ROWS):
        yield slice(start, min(start + _STRIP_ROWS, height))


def _apply(
    source: np.ndarray,
    rng: np.random.Generator,
    strength: FinishStrength,
    treatment: FinishTreatment,
) -> np.ndarray:
    """Run the treatments over ``source`` (which is consumed) and return uint8 pixels."""
    amount = _STRENGTH[strength]
    image = source
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
    ceiling = 254.0 if treatment in (FinishTreatment.MATTE, FinishTreatment.PRINT) else None
    return _quantise(image, ceiling)


def _unit_table(divisor: float) -> np.ndarray:
    """Float32 value of every uint8 level after ``(level - 128) / divisor``."""
    return (np.arange(256, dtype=np.float32) - 128.0) / divisor


_NOISE_TABLE: Final = _unit_table(32.0)
_FIBRE_TABLE: Final = _unit_table(28.0)
_FRACTION_TABLE: Final = np.arange(256, dtype=np.float32) / 255.0


def _noise_levels(shape: tuple[int, int], rng: np.random.Generator, scale: int) -> Image.Image:
    height, width = shape
    small = rng.normal(0.0, 1.0, (max(2, height // scale), max(2, width // scale)))
    small_image = Image.fromarray(np.uint8(np.clip(small * 32 + 128, 0, 255)), "L")
    return small_image.resize((width, height), Image.Resampling.BICUBIC)


def _paper(image: np.ndarray, rng: np.random.Generator, amount: float) -> np.ndarray:
    height, width, _ = image.shape
    coarse = np.asarray(_noise_levels((height, width), rng, max(16, min(width, height) // 54)))
    tooth = np.asarray(_noise_levels((height, width), rng, max(3, min(width, height) // 480)))
    fibre_small = rng.normal(
        0.0,
        1.0,
        (max(2, height // 28), max(2, width // 240)),
    )
    fibre_image = Image.fromarray(np.uint8(np.clip(fibre_small * 28 + 128, 0, 255)), "L")
    fibre_image = fibre_image.resize((width, height), Image.Resampling.BICUBIC)
    fibre_image = fibre_image.filter(ImageFilter.GaussianBlur(max(0.25, width / 2600)))
    fibres = np.asarray(fibre_image)
    for rows in _strips(height):
        strip = image[rows]
        luma = strip @ _LUMA
        light_weight = np.clip((luma - 24.0) / 180.0, 0.0, 1.0)
        dark_weight = 0.24 + 0.76 * light_weight
        texture = (
            _NOISE_TABLE[coarse[rows]] * 0.45
            + _NOISE_TABLE[tooth[rows]] * 0.32
            + _FIBRE_TABLE[fibres[rows]] * 0.23
        )
        factor = 1.0 + texture * (0.032 * amount) * dark_weight
        image[rows] = strip * factor[..., None]
    return image


def _halftone(image: np.ndarray, amount: float) -> np.ndarray:
    height, width, _ = image.shape
    gray_levels = np.empty((height, width), dtype=np.uint8)
    for rows in _strips(height):
        gray_levels[rows] = np.uint8(np.clip(image[rows] @ _LUMA, 0, 255))
    gray = Image.fromarray(gray_levels, "L")
    edge = np.asarray(gray.filter(ImageFilter.FIND_EDGES))
    smooth = np.asarray(gray.filter(ImageFilter.GaussianBlur(1.2)))
    angle = np.deg2rad(23.0)
    period = max(5.0, min(width, height) / 180.0)
    columns = np.arange(width) * np.cos(angle)
    rows_phase = np.arange(height) * np.sin(angle)
    result = np.empty(image.shape, dtype=np.float64)
    for rows in _strips(height):
        shadow = np.clip((168.0 - smooth[rows].astype(np.float32)) / 100.0, 0.0, 1.0)
        flat = np.clip((13.0 - edge[rows].astype(np.float32)) / 13.0, 0.0, 1.0)
        mask = shadow * flat
        if not mask.any():
            # Zero modulation leaves ``1.0 + dots * 0.0`` exactly 1.0 for every finite dot.
            result[rows] = image[rows]
            continue
        phase = ((columns + rows_phase[rows, None]) / period) % 1.0
        dots = np.cos(phase * 2.0 * np.pi)
        modulation = 1.0 + dots * 0.035 * amount * mask
        np.multiply(image[rows], modulation[..., None], out=result[rows])
    return result


def _min3x3(plane: np.ndarray) -> np.ndarray:
    """Edge-replicated 3x3 minimum of a uint8 plane (PIL ``MinFilter(3)``)."""
    padded = np.pad(plane, 1, mode="edge")
    vertical = np.minimum(np.minimum(padded[:-2], padded[1:-1]), padded[2:])
    return np.minimum(np.minimum(vertical[:, :-2], vertical[:, 1:-1]), vertical[:, 2:])


def _registration_and_bleed(
    image: np.ndarray, rng: np.random.Generator, amount: float
) -> np.ndarray:
    height, width, _ = image.shape
    # Displacements are specified at the 1280px working width and scale with it.
    scale = width / 1280.0
    max_shift = min(0.60 * scale, 0.46 * scale * amount)
    angle = rng.uniform(0.0, 2.0 * np.pi)
    shifts = [
        (0.0, 0.0),
        (np.cos(angle) * max_shift, np.sin(angle) * max_shift),
        (-np.sin(angle) * max_shift * 0.65, np.cos(angle) * max_shift * 0.65),
    ]
    planes = np.empty((3, height, width), dtype=np.uint8)
    skin_levels = np.empty((height, width), dtype=np.uint8)
    for rows in _strips(height):
        strip = image[rows]
        for index in range(3):
            planes[index, rows] = np.uint8(np.clip(strip[:, :, index], 0, 255))
        red, green, blue = strip[:, :, 0], strip[:, :, 1], strip[:, :, 2]
        luma_source = strip @ _LUMA
        skin = (
            (red > green + 10.0)
            & (red > blue + 15.0)
            & (green >= blue * 0.88)
            & (luma_source > 62.0)
            & (luma_source < 246.0)
        )
        skin_levels[rows] = np.uint8(skin) * 255
    for index, (dx, dy) in enumerate(shifts):
        if dx or dy:
            padded = Image.fromarray(np.pad(planes[index], ((2, 2), (2, 2)), mode="edge"), "L")
            padded = padded.transform(
                (width + 4, height + 4),
                Image.Transform.AFFINE,
                (1, 0, -dx, 0, 1, -dy),
                resample=Image.Resampling.BILINEAR,
                fillcolor=0,
            )
            planes[index] = np.asarray(padded.crop((2, 2, width + 2, height + 2)))
    # Red/yellow skin receives progressively less channel displacement. A soft
    # blur around the chroma mask avoids a visible boundary at cheeks and hands.
    skin_mask = Image.fromarray(skin_levels, "L")
    protected_levels = np.asarray(
        skin_mask.filter(ImageFilter.GaussianBlur(max(1.0, width / 420.0)))
    )
    gray_levels = np.empty((height, width), dtype=np.uint8)
    for rows in _strips(height):
        protected = _FRACTION_TABLE[protected_levels[rows]][..., None]
        shifted = np.empty((rows.stop - rows.start, width, 3), dtype=np.float32)
        for index in range(3):
            shifted[:, :, index] = planes[index, rows]
        shifted *= 1.0 - protected
        strip = image[rows]
        strip *= protected
        strip += shifted
        gray_levels[rows] = np.uint8(np.clip(strip.mean(axis=2), 0, 255))
    # Very restrained ink spread along dark contours, reduced on broad flat regions.
    eroded = _min3x3(gray_levels)
    spread = 0.035 * amount
    for rows in _strips(height):
        strip = image[rows]
        dark_ink = np.clip((150.0 - gray_levels[rows].astype(np.float32)) / 130.0, 0.0, 1.0)
        weight = spread * dark_ink[..., None]
        edge_spread = np.minimum(strip, eroded[rows].astype(np.float32)[..., None] * 1.02)
        strip *= 1.0 - weight
        edge_spread *= weight
        strip += edge_spread
    return image


def _matte(image: np.ndarray, amount: float) -> np.ndarray:
    amount = min(2.0, max(0.0, amount))
    blend = min(1.0, amount)
    for rows in _strips(image.shape[0]):
        strip = image[rows]
        luma = strip @ _LUMA
        contrast = (strip - 127.5) * 1.035 + 127.5
        # A restrained S curve and lifted shadows; paper tint is strongest in highlights.
        normalized = np.clip((contrast - 128.0) / 128.0, -1.0, 1.0)
        curved = 128.0 + normalized * (1.0 + 0.12 * (1.0 - normalized**2)) * 128.0
        lifted = curved * 0.982 + 4.2 * amount
        saturation = 1.0 - 0.055 * amount
        gray = luma[..., None]
        matte = gray + (lifted - gray) * saturation
        highlight = np.clip((luma - 155.0) / 100.0, 0.0, 1.0)[..., None]
        matte = matte * (1.0 - 0.026 * amount * highlight) + _PAPER_TINT * (
            0.026 * amount * highlight
        )
        image[rows] = strip * (1.0 - blend) + matte * blend
    return image


def _grain(image: np.ndarray, rng: np.random.Generator, amount: float) -> np.ndarray:
    # Noise is float64, so a float32 image is promoted exactly as in a whole-array sum.
    result = image if image.dtype == np.float64 else np.empty(image.shape, dtype=np.float64)
    width = image.shape[1]
    for rows in _strips(image.shape[0]):
        strip = image[rows]
        luma = strip @ _LUMA
        weight = np.clip(0.38 + luma / 255.0 * 0.62, 0.38, 1.0)
        noise = rng.normal(0.0, 2.0 * amount, (luma.shape[0], width)) * weight
        np.add(strip, noise[..., None], out=result[rows])
    return result


def _quantise(image: np.ndarray, ceiling: float | None) -> np.ndarray:
    pixels = np.empty(image.shape, dtype=np.uint8)
    for rows in _strips(image.shape[0]):
        strip = image[rows]
        if ceiling is not None:
            strip = np.minimum(strip, ceiling)
        pixels[rows] = np.clip(np.rint(strip), 0, 255)
    return pixels
