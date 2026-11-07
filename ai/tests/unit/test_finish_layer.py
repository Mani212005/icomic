from __future__ import annotations

from hashlib import sha256
from io import BytesIO

import numpy as np
import pytest
from fastapi.testclient import TestClient
from PIL import Image

from app.adapters.storage.fake import FakeStorageAdapter
from app.main import create_app
from app.services import finish_layer
from app.services.finish_layer import (
    FinishStrength,
    FinishTreatment,
    finish_image,
    finish_pixels,
    page_seed,
)
from app.services.pdf import _finished_display_jpeg


def _encode(image: Image.Image, image_format: str = "PNG") -> bytes:
    output = BytesIO()
    image.save(output, format=image_format)
    return output.getvalue()


def _decode(image_bytes: bytes) -> Image.Image:
    with Image.open(BytesIO(image_bytes)) as image:
        return image.copy()


def test_seeded_output_is_byte_identical_and_page_seed_is_stable() -> None:
    source = _encode(Image.new("RGB", (83, 117), (132, 87, 46)))
    first = finish_image(source, seed="page-17")
    second = finish_image(source, seed="page-17")
    assert first == second
    assert page_seed("page-17") == page_seed("page-17")
    assert page_seed("page-17") != page_seed("page-18")


def test_distinct_page_seeds_produce_distinct_page_derivatives() -> None:
    source = _encode(Image.new("RGB", (128, 192), (142, 98, 72)))
    first = finish_image(source, seed=page_seed("page-a"), strength=FinishStrength.HEAVY)
    second = finish_image(source, seed=page_seed("page-b"), strength=FinishStrength.HEAVY)
    assert first != second


def test_plus_and_heavy_are_seeded_and_distinct_presets() -> None:
    source = _encode(Image.new("RGB", (83, 117), (132, 87, 46)))
    plus_a = finish_image(source, seed="page-17", strength=FinishStrength.PLUS)
    plus_b = finish_image(source, seed="page-17", strength=FinishStrength.PLUS)
    heavy = finish_image(source, seed="page-17", strength=FinishStrength.HEAVY)
    assert plus_a == plus_b
    assert heavy != plus_a


def test_off_returns_exact_input_bytes() -> None:
    source = _encode(Image.new("RGB", (17, 21), (255, 255, 255)))
    assert finish_image(source, seed=1, strength=FinishStrength.OFF) == source


def test_finish_preserves_size_mode_and_alpha() -> None:
    rgba = Image.new("RGBA", (79, 53), (120, 70, 33, 173))
    source = _encode(rgba)
    finished = _decode(finish_image(source, seed=3))
    assert finished.size == rgba.size
    assert finished.mode == "RGBA"
    assert finished.getchannel("A").tobytes() == rgba.getchannel("A").tobytes()

    grayscale = Image.new("L", (31, 49), 140)
    gray_finished = _decode(finish_image(_encode(grayscale), seed=4))
    assert gray_finished.mode == "L"
    assert gray_finished.size == grayscale.size


def test_art_mask_keeps_pixels_outside_art_unchanged() -> None:
    original = Image.new("RGB", (64, 48), (80, 90, 100))
    mask = Image.new("L", original.size, 0)
    for y in range(8, 40):
        for x in range(9, 55):
            mask.putpixel((x, y), 255)
    finished = _decode(finish_image(_encode(original), seed=9, art_mask=_encode(mask)))
    for y in range(original.height):
        for x in range(original.width):
            if mask.getpixel((x, y)) == 0:
                assert finished.getpixel((x, y)) == original.getpixel((x, y))


def test_odd_dimensions_are_supported() -> None:
    source = _encode(Image.new("RGB", (127, 91), (110, 120, 130)))
    finished = _decode(finish_image(source, seed=41))
    assert finished.size == (127, 91)


def test_print_treatment_has_paper_white_instead_of_pure_white() -> None:
    source = _encode(Image.new("RGB", (35, 29), (255, 255, 255)))
    finished = _decode(finish_image(source, seed=1))
    assert max(finished.getextrema()[channel][1] for channel in range(3)) < 255


def test_finish_page_endpoint_stores_a_deterministic_immutable_derivative() -> None:
    storage = FakeStorageAdapter()
    app = create_app()
    app.state.storage_adapter = storage
    source = _encode(Image.new("RGB", (83, 117), (132, 87, 46)))
    app.state.media_fetch_adapter = type("Media", (), {"fetch": lambda self, _url: source})()
    payload = {
        "page_id": "2e6aa8c0-318d-4571-b1cd-2be622d35b48",
        "image_url": "master.png",
        "finish_preset": "print-heavy",
    }
    with TestClient(app) as client:
        first = client.post("/internal/v1/finish-page-image", json=payload)
        second = client.post("/internal/v1/finish-page-image", json=payload)
    assert first.status_code == second.status_code == 200
    assert first.json()["image_url"] == second.json()["image_url"]
    key = first.json()["image_url"].split("/storage/fake/")[-1]
    derivative = storage.get(key)
    assert derivative.startswith(b"\xff\xd8")
    assert sha256(derivative).hexdigest() in key


def test_registration_offsets_stay_subpixel_to_one_pixel_at_work_width() -> None:
    image = Image.new("RGB", (1280, 32), (0, 0, 0))
    for y in range(image.height):
        for x in range(640, image.width):
            image.putpixel((x, y), (255, 255, 255))
    finished = _decode(
        finish_image(
            _encode(image),
            seed=17,
            strength=FinishStrength.PRINT,
            treatment=FinishTreatment.REGISTRATION,
        )
    )
    edges = []
    for channel in range(3):
        pixels = finished.getchannel(channel)
        crossing = next(x for x in range(638, 643) if pixels.getpixel((x, 16)) >= 128)
        edges.append(crossing)
    assert max(edges) - min(edges) <= 1


def _painted(width: int, height: int) -> Image.Image:
    rng = np.random.default_rng(11)
    ramp = np.linspace(40, 230, width, dtype=np.float32)
    pixels = np.stack([ramp[None, :].repeat(height, 0)] * 3, axis=2)
    pixels[: height // 3, : width // 3] = (226, 168, 138)  # skin-like block
    pixels[height // 2 :, width // 2 :] = (20, 20, 30)  # dark flat block
    pixels += rng.normal(0, 4, pixels.shape)
    return Image.fromarray(np.clip(pixels, 0, 255).astype(np.uint8), "RGB")


@pytest.mark.parametrize("treatment", list(FinishTreatment))
def test_output_does_not_depend_on_strip_height(monkeypatch, treatment) -> None:
    source = _encode(_painted(131, 97))  # odd sizes: strips never divide the page evenly
    expected = finish_image(source, seed="strips", strength="plus", treatment=treatment)
    for rows in (1, 7, 1000):
        monkeypatch.setattr(finish_layer, "_STRIP_ROWS", rows)
        assert finish_image(source, seed="strips", strength="plus", treatment=treatment) == expected


def test_min3x3_matches_pil_min_filter_including_borders() -> None:
    from PIL import ImageFilter

    plane = np.random.default_rng(5).integers(0, 256, (37, 53), dtype=np.uint8)
    expected = np.asarray(Image.fromarray(plane, "L").filter(ImageFilter.MinFilter(3)))
    assert np.array_equal(finish_layer._min3x3(plane), expected)


def test_pdf_display_copy_skips_the_lossless_round_trip_without_changing_pixels() -> None:
    source = _encode(_painted(96, 128))
    seed = page_seed("page-3/print")
    finished = finish_image(source, seed=seed, strength=FinishStrength.PRINT)
    via_png = BytesIO()
    _decode(finished).convert("RGB").save(via_png, format="JPEG", quality=92, optimize=True)
    assert _finished_display_jpeg(source, seed, FinishStrength.PRINT) == via_png.getvalue()


def test_pdf_display_copy_keeps_lossy_source_generation_loss() -> None:
    source = _encode(_painted(96, 128), "JPEG")
    seed = page_seed("page-3/print")
    finished = finish_image(source, seed=seed, strength=FinishStrength.PLUS)
    via_jpeg = BytesIO()
    _decode(finished).convert("RGB").save(via_jpeg, format="JPEG", quality=92, optimize=True)
    assert _finished_display_jpeg(source, seed, FinishStrength.PLUS) == via_jpeg.getvalue()


def test_finish_pixels_reports_unchanged_for_off_and_empty_mask() -> None:
    source = _encode(_painted(40, 40))
    assert finish_pixels(source, seed=1, strength="off") is None
    empty = _encode(Image.new("L", (40, 40), 0))
    assert finish_pixels(source, seed=1, art_mask=empty) is None
