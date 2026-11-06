"""Regenerate the offline finish-layer comparison sheets and page variants."""

from __future__ import annotations

import argparse
import sys
from io import BytesIO
from pathlib import Path

from PIL import Image, ImageDraw, ImageFont

sys.path.insert(0, str(Path(__file__).resolve().parents[2] / "ai"))

from app.services.finish_layer import (  # noqa: I001
    FinishStrength,
    FinishTreatment,
    finish_image,
    page_seed,
)


DEFAULT_SOURCE = Path("/home/mani/icomic-artifacts/long-run-final-2/images")
DEFAULT_OUTPUT = Path("/home/mani/icomic-artifacts/finish-layer")
PAGES = {
    "storm": ("comic-12.png", (790, 1330, 1150, 1690)),
    "bright": ("comic-04.png", (180, 1050, 540, 1410)),
    "face": ("comic-01.png", (160, 190, 520, 550)),
}
TREATMENTS = [
    FinishTreatment.PAPER,
    FinishTreatment.HALFTONE,
    FinishTreatment.REGISTRATION,
    FinishTreatment.MATTE,
    FinishTreatment.GRAIN,
    FinishTreatment.PRINT,
]
_CACHE: dict[tuple[str, FinishTreatment, FinishStrength], Image.Image] = {}


def _load_font(size: int) -> ImageFont.ImageFont:
    try:
        return ImageFont.truetype("DejaVuSans.ttf", size)
    except OSError:
        return ImageFont.load_default()


def _open(data: bytes) -> Image.Image:
    with Image.open(BytesIO(data)) as image:
        return image.convert("RGB")


def _fit(image: Image.Image, size: tuple[int, int]) -> Image.Image:
    return image.copy().resize(size, Image.Resampling.LANCZOS)


def _finish(
    source: bytes, page_name: str, strength: FinishStrength, treatment: FinishTreatment
):
    key = (page_name, treatment, strength)
    if key not in _CACHE:
        _CACHE[key] = _open(
            finish_image(
                source,
                seed=page_seed(page_name),
                strength=strength,
                treatment=treatment,
            )
        )
    return _CACHE[key]


def make_exploration_sheet(source_dir: Path, output: Path) -> None:
    treatments = [
        ("a · paper fibre + tooth", FinishTreatment.PAPER, FinishStrength.PRINT),
        ("b · shadow halftone", FinishTreatment.HALFTONE, FinishStrength.PRINT),
        (
            "c · registration + edge bleed",
            FinishTreatment.REGISTRATION,
            FinishStrength.PRINT,
        ),
        ("d · matte inks + tone curve", FinishTreatment.MATTE, FinishStrength.PRINT),
        ("e · scan grain", FinishTreatment.GRAIN, FinishStrength.PRINT),
        ("f · combined · light", FinishTreatment.PRINT, FinishStrength.LIGHT),
        ("f · combined · print", FinishTreatment.PRINT, FinishStrength.PRINT),
    ]
    margin, label_width = 24, 225
    full_size, crop_size = (185, 278), (360, 360)
    gap = 18
    width = margin * 2 + label_width + full_size[0] * 2 + crop_size[0] * 2 + gap * 3
    row_height = max(full_size[1], crop_size[1]) + 40
    section_height = row_height * len(treatments) + 56
    canvas = Image.new(
        "RGB", (width, margin * 2 + section_height * len(PAGES)), "#e9e3d8"
    )
    draw = ImageDraw.Draw(canvas)
    title_font, label_font = _load_font(26), _load_font(17)

    for page_index, (scene, (filename, crop_box)) in enumerate(PAGES.items()):
        source = (source_dir / filename).read_bytes()
        original = _open(source)
        crop = original.crop(crop_box)
        top = margin + page_index * section_height
        draw.text(
            (margin, top),
            f"{scene.upper()} · {filename}",
            fill="#211d19",
            font=title_font,
        )
        draw.text(
            (margin + label_width, top + 31),
            "FULL PAGE: original | treatment    DETAIL CROP AT SOURCE PIXELS: original | treatment",
            fill="#50483f",
            font=label_font,
        )
        for treatment_index, (label, treatment, strength) in enumerate(treatments):
            row_top = top + 56 + treatment_index * row_height
            candidate = _finish(source, filename, strength, treatment)
            candidate_crop = candidate.crop(crop_box)
            draw.text((margin, row_top + 7), label, fill="#211d19", font=label_font)
            x = margin + label_width
            for panel in (
                _fit(original, full_size),
                _fit(candidate, full_size),
                crop,
                candidate_crop,
            ):
                canvas.paste(panel, (x, row_top))
                x += panel.width + gap
            draw.line(
                (
                    margin,
                    row_top + row_height - 13,
                    width - margin,
                    row_top + row_height - 13,
                ),
                fill="#c6bbae",
                width=1,
            )

    canvas.save(output, quality=92)


def make_candidate_sheet(source_dir: Path, output: Path) -> None:
    selected = ["face", "storm"]
    width, margin, title_height = 2700, 28, 52
    page_size = (360, 540)
    row_height = 650
    canvas = Image.new(
        "RGB", (width, margin * 2 + len(selected) * row_height), "#e9e3d8"
    )
    draw = ImageDraw.Draw(canvas)
    title_font, label_font = _load_font(26), _load_font(18)
    for index, scene in enumerate(selected):
        filename, crop_box = PAGES[scene]
        source = (source_dir / filename).read_bytes()
        original = _open(source)
        light = _finish(source, filename, FinishStrength.LIGHT, FinishTreatment.PRINT)
        printed = _finish(source, filename, FinishStrength.PRINT, FinishTreatment.PRINT)
        y = margin + index * row_height
        draw.text(
            (margin, y),
            f"{scene.upper()} · {filename}",
            fill="#211d19",
            font=title_font,
        )
        panels = [
            ("ORIGINAL", original),
            ("PRINT · LIGHT", light),
            ("PRINT · PRINT", printed),
        ]
        x = margin
        for label, image in panels:
            draw.text((x, y + title_height), label, fill="#50483f", font=label_font)
            canvas.paste(_fit(image, page_size), (x, y + title_height + 28))
            canvas.paste(
                image.crop(crop_box), (x + page_size[0] + 20, y + title_height + 28)
            )
            x += 2 * page_size[0] + 54
    canvas.save(output, quality=92)


def _ladder_variants(source: bytes, filename: str) -> list[tuple[str, Image.Image]]:
    original = _open(source)
    return [
        ("ORIGINAL", original),
        (
            "PRINT · CURRENT",
            _finish(source, filename, FinishStrength.PRINT, FinishTreatment.PRINT),
        ),
        (
            "PRINT-PLUS",
            _finish(source, filename, FinishStrength.PLUS, FinishTreatment.PRINT),
        ),
        (
            "PRINT-HEAVY",
            _finish(source, filename, FinishStrength.HEAVY, FinishTreatment.PRINT),
        ),
    ]


def make_strength_ladder_sheet(source_dir: Path, output: Path) -> None:
    selected = ["face", "storm"]
    margin, page_size, crop_size = 28, (320, 480), (360, 360)
    gap, section_height = 22, 900
    column_width = page_size[0] + gap + crop_size[0] + 26
    width = margin * 2 + 4 * column_width
    canvas = Image.new(
        "RGB", (width, margin * 2 + len(selected) * section_height), "#e9e3d8"
    )
    draw = ImageDraw.Draw(canvas)
    title_font, label_font = _load_font(26), _load_font(18)

    for page_index, scene in enumerate(selected):
        filename, crop_box = PAGES[scene]
        source = (source_dir / filename).read_bytes()
        y = margin + page_index * section_height
        draw.text(
            (margin, y),
            f"{scene.upper()} · {filename}",
            fill="#211d19",
            font=title_font,
        )
        variants = _ladder_variants(source, filename)
        for index, (label, image) in enumerate(variants):
            x = margin + index * column_width
            draw.text((x, y + 40), label, fill="#211d19", font=label_font)
            draw.text((x, y + 68), "FULL PAGE", fill="#50483f", font=label_font)
            canvas.paste(_fit(image, page_size), (x, y + 96))
            draw.text(
                (x + page_size[0] + gap, y + 68),
                "100% CROP",
                fill="#50483f",
                font=label_font,
            )
            canvas.paste(image.crop(crop_box), (x + page_size[0] + gap, y + 96))
    canvas.save(output, quality=94)


def make_screen_size_sheet(source_dir: Path, output: Path) -> None:
    """Create an exactly 1280px-wide review sheet with unscaled detail crops."""
    selected = ["face", "storm"]
    width, margin, column_width = 1280, 20, 295
    gap = 7
    section_height = 860
    canvas = Image.new("RGB", (width, 44 + len(selected) * section_height), "#e9e3d8")
    draw = ImageDraw.Draw(canvas)
    title_font, label_font = _load_font(23), _load_font(16)
    for page_index, scene in enumerate(selected):
        filename, crop_box = PAGES[scene]
        source = (source_dir / filename).read_bytes()
        variants = _ladder_variants(source, filename)
        top = 44 + page_index * section_height
        draw.text(
            (margin, top),
            f"{scene.upper()} · SCREEN REVIEW · 1280 PX WIDE",
            fill="#211d19",
            font=title_font,
        )
        for index, (label, image) in enumerate(variants):
            x = margin + index * (column_width + gap)
            draw.text((x, top + 35), label, fill="#211d19", font=label_font)
            draw.text((x, top + 59), "PAGE PREVIEW", fill="#50483f", font=label_font)
            canvas.paste(_fit(image, (column_width, 443)), (x, top + 83))
            draw.text(
                (x, top + 535),
                "100% DETAIL · SOURCE PIXELS",
                fill="#50483f",
                font=label_font,
            )
            detail = image.crop(crop_box)
            left = (detail.width - column_width) // 2
            upper = (detail.height - column_width) // 2
            canvas.paste(
                detail.crop((left, upper, left + column_width, upper + column_width)),
                (x, top + 559),
            )
    canvas.save(output, quality=94)


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--source-dir", type=Path, default=DEFAULT_SOURCE)
    parser.add_argument("--output-dir", type=Path, default=DEFAULT_OUTPUT)
    parser.add_argument(
        "--refresh-phase-one",
        action="store_true",
        help="regenerate the original component sheets too",
    )
    args = parser.parse_args()
    args.output_dir.mkdir(parents=True, exist_ok=True)
    if args.refresh_phase_one:
        make_exploration_sheet(
            args.source_dir, args.output_dir / "treatment-comparison.jpg"
        )
        make_candidate_sheet(
            args.source_dir, args.output_dir / "candidate-comparison.jpg"
        )
    make_strength_ladder_sheet(
        args.source_dir, args.output_dir / "strength-ladder-comparison.jpg"
    )
    make_screen_size_sheet(
        args.source_dir, args.output_dir / "strength-ladder-screen-1280.jpg"
    )
    for scene in ("face", "storm"):
        filename, _ = PAGES[scene]
        source = (args.source_dir / filename).read_bytes()
        stem = Path(filename).stem
        for strength in (
            FinishStrength.PRINT,
            FinishStrength.PLUS,
            FinishStrength.HEAVY,
        ):
            target = args.output_dir / f"{stem}-print-{strength.value}.png"
            if (
                strength in (FinishStrength.PLUS, FinishStrength.HEAVY)
                or not target.exists()
            ):
                target.write_bytes(
                    finish_image(source, seed=page_seed(filename), strength=strength)
                )
        original_target = args.output_dir / f"{stem}-original.png"
        if not original_target.exists():
            original_target.write_bytes(source)
    print(args.output_dir)


if __name__ == "__main__":
    main()
