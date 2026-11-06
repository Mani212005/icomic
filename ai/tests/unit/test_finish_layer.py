from __future__ import annotations

from io import BytesIO

from PIL import Image

from app.services.finish_layer import (
    FinishStrength,
    FinishTreatment,
    finish_image,
    page_seed,
)


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
