"""Stored vector geometry, Unicode, review guards and legacy stream regressions."""

import json
import re
import subprocess
import zipfile
import zlib
from io import BytesIO
from pathlib import Path

import numpy as np
import pytest
from PIL import Image

from app.adapters.storage.fake import FakeStorageAdapter
from app.gen.models import LetteringDocument, PageExportItem
from app.services.lettering import layout_page
from app.services.lettering.pdf import PdfLettering, bubble_path
from app.services.pdf import PdfAssemblyService, PdfWriter
from tests.image_fixtures import placeholder_png

FIXTURES = Path(__file__).parents[1] / "fixtures/lettering"


def page(name="speech-tail", **changes):
    doc = LetteringDocument.model_validate_json((FIXTURES / f"{name}.input.json").read_bytes())
    layout = layout_page(doc)
    state = {
        "version": "page-lettering-v1",
        "input": doc.model_dump(mode="json"),
        "overrides": {},
        "zone_overrides": {},
        "layout": layout.model_dump(mode="json"),
    }
    return dict(
        page_number=1,
        lettering_mode="overlay",
        lettering=state,
        image_bytes=placeholder_png(600, 849),
        **changes,
    )


def build(pages):
    writer = PdfWriter()
    return writer.build_pdf("test", "Lettering", False, pages), writer


def page_streams(pdf):
    objects = dict(re.findall(rb"(\d+) 0 obj\n(.*?)\nendobj", pdf, re.DOTALL))
    pages = [obj for obj in objects.values() if b"/Type /Page /Parent" in obj]
    return [
        zlib.decompress(
            re.search(
                rb"stream\n(.*?)\nendstream",
                objects[re.search(rb"/Contents (\d+) 0 R", p)[1]],
                re.DOTALL,
            )[1]
        )
        for p in pages
    ]


def extract(pdf):
    return subprocess.run(
        ["pdftotext", "-", "-"], input=pdf, capture_output=True, check=True
    ).stdout.decode()


def test_vector_operator_golden_and_exact_positioning():
    p = page()
    layout = layout_page(LetteringDocument.model_validate(p["lettering"]["input"]))
    renderer = PdfLettering(lambda _: 1, [layout])
    actual = renderer.draw(layout, 40, 60, 500, 707)
    assert actual + b"\n" == (FIXTURES / "pdf-operators.golden.txt").read_bytes()
    assert b"W n\n1 g 0 G" in actual  # fill and stroke clipped to silhouette
    assert b" c\n" in actual and b" Tm <" in actual and b" Tj\n" in actual
    assert b"/ActualText <FEFF" in actual
    path = bubble_path(layout.bubbles[0], layout.page_aspect)
    assert sum(p.endswith(" m") for p in path) == 1  # tail and ellipse have one outline
    pdf, writer = build([p])
    assert not writer.warnings
    assert pdf.count(b"/Subtype /Image") == 2  # cover and original art only
    assert pdf.count(b"/FontFile2") == 1
    assert b"/CIDToGIDMap" in pdf and b"/ToUnicode" in pdf
    assert page_streams(pdf)[1].count(b" BDC") == len(layout.bubbles[0].lines)


def test_finishing_art_leaves_vector_lettering_operators_unchanged():
    raw_pdf, _ = build([page()])
    finished_pdf, _ = build([page(finish_preset="print-heavy", page_id="vector-fixture")])
    assert page_streams(raw_pdf)[1] == page_streams(finished_pdf)[1]


@pytest.mark.parametrize("name", ["unicode-latin", "unicode-devanagari"])
def test_unicode_subset_fonts_have_extractable_logical_text(name):
    p = page(name)
    pdf, writer = build([p])
    assert not writer.warnings
    text = extract(pdf)
    for bubble in p["lettering"]["layout"]["bubbles"]:
        for line in bubble["lines"]:
            assert line["text"] in text
    assert b"/ToUnicode" in pdf and b"/FontFile2" in pdf
    assert b"/BaseFont /" in pdf and re.search(rb"/[A-Z]{6}\+", pdf)


@pytest.mark.parametrize("mutation", ["text", "zone", "override", "digest", "source"])
def test_stale_layout_is_flagged_without_relayout(mutation, monkeypatch):
    p = page()
    state = p["lettering"]
    if mutation == "text":
        state["input"]["panels"][0]["utterances"][0]["text"] = "Changed source"
    elif mutation == "zone":
        state["input"]["panels"][0]["reserved_zones"][0]["height"] *= 0.5
    elif mutation == "override":
        state["overrides"][state["input"]["panels"][0]["utterances"][0]["utterance_id"]] = {
            "center": {"x": 0.3, "y": 0.2}
        }
    elif mutation == "digest":
        state["layout"].pop("source_digest")
    else:
        p["lettering_stale"] = True
    monkeypatch.setattr(
        "app.services.lettering.layout_page", lambda *_: pytest.fail("PDF must never relayout")
    )
    pdf, writer = build([p])
    assert writer.warnings[0].code == "stale_lettering"
    assert "LETTERING NOT EXPORTED" in extract(pdf)
    assert b"/FontFile2" not in pdf


@pytest.mark.parametrize(
    "flag",
    [
        "overflow",
        "collision",
        "out_of_panel",
        "missing_zone",
        "no_calm_zone",
        "unsupported_glyph",
    ],
)
def test_review_flags_produce_typed_warning_and_pdf_note(flag):
    p = page()
    p["lettering"]["layout"]["issues"] = [
        {"panel_id": "panel-1", "utterance_id": "u-1", "flag": flag}
    ]
    pdf, writer = build([p])
    assert writer.warnings[0].code == "lettering_review_required"
    assert writer.warnings[0].flags[0].root == flag
    assert flag in extract(pdf)
    assert b"/FontFile2" not in pdf


def test_no_calm_omission_warns_but_exports_other_safe_bubbles():
    p = page()
    p["lettering"]["layout"]["issues"] = [
        {"panel_id": "panel-1", "utterance_id": "withheld", "flag": "no_calm_zone"}
    ]
    pdf, writer = build([p])
    assert writer.warnings[0].flags[0].root == "no_calm_zone"
    assert "Some lettering was withheld" in extract(pdf)
    assert "LETTERING REVIEW REQUIRED" in extract(pdf)
    assert "LETTERING NOT EXPORTED" not in extract(pdf)
    assert b"/FontFile2" in pdf


def test_missing_layer_is_visible_and_baked_pages_ignore_any_layer():
    p = page()
    p.pop("lettering")
    _, writer = build([p])
    assert writer.warnings[0].code == "stale_lettering"
    legacy = {"page_number": 1, "image_bytes": p["image_bytes"]}
    expected, _ = build([legacy])
    for mode in [None, "baked"]:
        contaminated = page()
        contaminated["lettering_mode"] = mode
        actual, _ = build([contaminated])
        assert page_streams(actual)[1:] == page_streams(expected)[1:]
    # Lock the pre-stage-4 comic content stream, including legacy transcript behavior.
    assert page_streams(expected)[1] == (FIXTURES / "pdf-legacy.golden.txt").read_bytes().rstrip(
        b"\n"
    )


def test_legacy_transcript_stream_matches_pre_overlay_writer():
    data = {
        "page_number": 1,
        "image_bytes": placeholder_png(600, 849),
        "panels": [
            {
                "panel_number": 1,
                "narration": "Before sunrise.",
                "dialogue": "Where did you come from?",
            }
        ],
    }
    pdf, _ = build([data])
    assert page_streams(pdf)[1] == (
        FIXTURES / "pdf-legacy-transcript.golden.txt"
    ).read_bytes().rstrip(b"\n")


def test_overlay_preserves_image_xobject_bytes():
    p = page()
    before, _ = build([{**p, "lettering_mode": "baked"}])
    after, _ = build([p])

    def images(pdf):
        return [
            obj
            for obj in re.findall(rb"\d+ 0 obj\n(.*?)\nendobj", pdf, re.DOTALL)
            if b"/Subtype /Image" in obj
        ]

    assert images(before) == images(after)


def test_mixed_document_preserves_legacy_content_and_embeds_font_once():
    baked = {"page_number": 1, "image_bytes": placeholder_png(600, 849)}
    control, _ = build([baked, baked, baked])
    overlay = page()
    pdf, _ = build([baked, overlay, overlay])
    assert page_streams(control)[1] == page_streams(pdf)[1]
    assert len(page_streams(pdf)) == 4
    assert pdf.count(b"/FontFile2") == 1
    assert extract(pdf).count(overlay["lettering"]["layout"]["bubbles"][0]["lines"][0]["text"]) == 2


def test_overlay_adds_under_30_kb_and_preserves_original_image_bytes():
    p = page("unicode-devanagari")
    p["original_image_bytes"] = p["image_bytes"] + b"source-provenance"
    baseline, _ = build([{**p, "lettering_mode": "baked"}])
    pdf, _ = build([p])
    assert len(pdf) - len(baseline) < 30_000
    assert p["original_image_bytes"] in pdf and p["image_bytes"] + b"source-provenance" in baseline


def test_real_probe_layout_pdf_has_extractable_lines_and_allows_image_payloads():
    fixture = FIXTURES / "nia-brass-bird-lettering.state"
    state = json.loads(fixture.read_text())
    art = (FIXTURES / "nia-brass-bird-raw.jpg").read_bytes()
    page_data = {
        "page_number": 1,
        "page_id": state["input"]["page_id"],
        "image_bytes": art,
        "original_image_bytes": art,
        "lettering_mode": "overlay",
        "finish_preset": "print-heavy",
        "lettering": state,
    }

    pdf, writer = build([page_data])
    extracted = " ".join(extract(pdf).split())
    lines = [
        line["text"]
        for bubble in state["layout"]["bubbles"]
        for line in bubble["lines"]
        if line.get("text")
    ]

    assert not writer.warnings
    # Text extraction follows page geometry, not the source array's panel order.
    assert " ".join(lines) not in extracted
    assert all(line in extracted for line in lines)
    assert len(pdf) > 500_000  # The old per-page cap rejected valid art and attachments.

    import sys

    sys.path.insert(0, str(Path(__file__).parents[3] / "scripts/e2e"))
    from comic_e2e import verify_overlay_pdf

    verify_overlay_pdf(pdf, [state["layout"]], 1)
    verify_overlay_pdf(pdf + b" " * 2_000_000, [state["layout"]], 1)


def test_live_3_text_free_art_relayouts_and_exports_offline():
    from app.gen.models import LetteringDocument
    from app.services.lettering import layout_page

    fixture = FIXTURES / "live-3/lettering-01.json"
    state = json.loads(fixture.read_text())
    art = (FIXTURES / "live-3/nia-brass-bird.webp").read_bytes()
    finished = (FIXTURES / "live-3/finished-art-01.jpg").read_bytes()
    document = LetteringDocument.model_validate(state["input"])
    layout = layout_page(document, art_image=art)
    state["layout"] = layout.model_dump(mode="json")
    page_data = {
        "page_number": 1,
        "page_id": document.page_id,
        "image_bytes": finished,
        "original_image_bytes": art,
        "lettering_mode": "overlay",
        "finish_preset": "print-heavy",
        "lettering": state,
    }

    pdf, writer = build([page_data])
    extracted = " ".join(extract(pdf).split())
    lines = [line.text for bubble in layout.bubbles for line in bubble.lines]

    assert layout.bubbles
    assert not {
        "collision",
        "outside_reserved_zone",
        "overflow",
    } & {issue.flag.root for issue in layout.issues}
    assert all(line in extracted for line in lines)
    assert len(page_streams(pdf)) == 2
    assert len(pdf) > 100_000
    assert not writer.warnings and not layout.issues  # Scored placement keeps every line
    assert "REVIEW REQUIRED" not in extracted


@pytest.mark.parametrize("name", ["speech-tail", "narration", "thought", "unicode-devanagari"])
def test_pdf_render_matches_serialized_layout_raster(name, tmp_path):
    # The PNG export's rasterizer (stored glyph outlines and shape union, never shaping) must
    # match what poppler renders from the PDF's vector text.
    import importlib.util

    spec = importlib.util.spec_from_file_location(
        "sample", Path(__file__).parents[3] / "scripts/render_lettering_sample.py"
    )
    raster = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(raster)
    p = page(name)
    source = tmp_path / "source.png"
    source.write_bytes(p["image_bytes"])
    reference = tmp_path / "reference.png"
    pdf, _ = build([p])
    path = tmp_path / "page.pdf"
    path.write_bytes(pdf)
    subprocess.run(
        [
            "pdftoppm",
            "-f",
            "2",
            "-l",
            "2",
            "-scale-to-x",
            "1191",
            "-scale-to-y",
            "1684",
            "-png",
            "-singlefile",
            str(path),
            str(tmp_path / "pdf"),
        ],
        check=True,
        capture_output=True,
    )
    rendered = Image.open(tmp_path / "pdf.png").convert("RGB")
    # A4 artwork transform, read from the actual image placement operators.
    stream = page_streams(pdf)[1]
    m = re.search(rb"([\d.]+) 0 0 ([\d.]+) ([\d.]+) ([\d.]+) cm /Im1 Do", stream)
    w, h, x, y = map(float, m.groups())
    sx, sy = rendered.width / 595.28, rendered.height / 841.89
    art = rendered.crop(
        (round(x * sx), round((841.89 - y - h) * sy), round((x + w) * sx), round((841.89 - y) * sy))
    )
    Image.open(source).resize(art.size, Image.Resampling.LANCZOS).save(source)
    raster.render(source, p["lettering"]["layout"], reference)
    expected = Image.open(reference)
    # Only lettering region, so the unchanged background cannot hide displacement.
    boxes = [b["shape"]["bounds"] for b in p["lettering"]["layout"]["bubbles"]]
    bounds = (
        int(min(b["x"] for b in boxes) * art.width) - 3,
        int(min(b["y"] for b in boxes) * art.height) - 3,
        int(max(b["x"] + b["width"] for b in boxes) * art.width) + 3,
        int(max(b["y"] + b["height"] for b in boxes) * art.height) + 30,
    )
    error = np.abs(
        np.asarray(art.crop(bounds), dtype=float) - np.asarray(expected.crop(bounds), dtype=float)
    )
    assert error.mean() < 14
    assert (error > 80).mean() < 0.07


def png_zip(*pages):
    """Export pages (dicts as built by ``page``) as a PNG ZIP; returns files, manifest, warnings."""
    storage = FakeStorageAdapter()
    items = [
        PageExportItem(
            page_number=number,
            image_url=storage.put(f"pages/{number}.png", p["image_bytes"], "image/png"),
            lettering_mode=p["lettering_mode"],
            lettering=p.get("lettering"),
            lettering_stale=p.get("lettering_stale", False),
        )
        for number, p in enumerate(pages, start=1)
    ]
    url, _, count, warnings = PdfAssemblyService(storage).assemble_png_zip(
        "png-lettering", "Lettering", True, items
    )
    assert count == len(pages)
    with zipfile.ZipFile(BytesIO(storage.get(url))) as archive:
        files = {name: archive.read(name) for name in archive.namelist()}
    return files, json.loads(files["manifest.json"]), warnings


def lettering_bounds(layout, size, pad=8):
    """Pixel box around every bubble body, tail and thought-tail circle of a stored layout."""
    xs, ys = [], []
    for bubble in layout["bubbles"]:
        for shape in [bubble["shape"], *bubble["thought_tail"]]:
            box = shape["bounds"]
            xs += [box["x"], box["x"] + box["width"]]
            ys += [box["y"], box["y"] + box["height"]]
        xs += [point["x"] for point in bubble["tail"]]
        ys += [point["y"] for point in bubble["tail"]]
    return (
        max(0, int(min(xs) * size[0]) - pad),
        max(0, int(min(ys) * size[1]) - pad),
        min(size[0], int(max(xs) * size[0]) + pad),
        min(size[1], int(max(ys) * size[1]) + pad),
    )


@pytest.mark.parametrize("name", ["speech-tail", "narration", "thought", "unicode-devanagari"])
def test_png_zip_draws_stored_lettering_and_touches_only_the_bubbles(name):
    p = page(name)
    files, manifest, warnings = png_zip(p)
    assert not warnings
    entry = manifest["pages"][0]
    assert entry["lettering"] == "drawn"
    # The provider file stays untouched beside the lettered page.
    assert files[entry["source_file"]] == p["image_bytes"]
    assert entry["source_file"].startswith("sources/") and entry["converted_from_source"]
    source = np.asarray(Image.open(BytesIO(p["image_bytes"])).convert("RGB"), dtype=int)
    with Image.open(BytesIO(files["pages/page-001.png"])) as exported:
        assert exported.format == "PNG" and exported.size == (600, 849)
        lettered = np.asarray(exported.convert("RGB"), dtype=int)
    left, top, right, bottom = lettering_bounds(p["lettering"]["layout"], (600, 849))
    outside = np.ones(source.shape[:2], dtype=bool)
    outside[top:bottom, left:right] = False
    assert (lettered[outside] == source[outside]).all()  # artwork elsewhere is pixel-identical
    inside = np.abs(lettered - source)[top:bottom, left:right]
    assert (inside.sum(axis=2) > 200).sum() > 500  # bubble fill, outline and glyph ink are drawn
    assert (lettered[top:bottom, left:right] < 40).all(axis=2).sum() > 50  # black outline and text


def test_png_zip_lettering_is_deterministic():
    first, _, _ = png_zip(page())
    second, _, _ = png_zip(page())
    assert first["pages/page-001.png"] == second["pages/page-001.png"]


@pytest.mark.parametrize("mutation", ["stale", "missing", "review"])
def test_png_zip_withholds_stale_or_flagged_lettering_with_the_pdf_warning(mutation):
    p = page()
    if mutation == "stale":
        p["lettering_stale"] = True
    elif mutation == "missing":
        p.pop("lettering")
    else:
        p["lettering"]["layout"]["issues"] = [
            {"panel_id": "panel-1", "utterance_id": "u-1", "flag": "overflow"}
        ]
    files, manifest, warnings = png_zip(p)
    _, pdf_writer = build([p])
    assert [w.code for w in warnings] == [w.code for w in pdf_writer.warnings]
    assert warnings[0].page_number == 1
    entry = manifest["pages"][0]
    assert entry["lettering"] == "withheld"
    # Nothing is drawn, and the PNG is the provider's file itself.
    assert files["pages/page-001.png"] == p["image_bytes"]
    assert entry["source_file"] == entry["file"]


def test_png_zip_draws_safe_bubbles_and_warns_when_no_calm_zone_withheld_a_line():
    p = page()
    p["lettering"]["layout"]["issues"] = [
        {"panel_id": "panel-1", "utterance_id": "withheld", "flag": "no_calm_zone"}
    ]
    files, manifest, warnings = png_zip(p)
    assert [w.code for w in warnings] == ["lettering_review_required"]
    assert manifest["pages"][0]["lettering"] == "drawn"
    assert files["pages/page-001.png"] != p["image_bytes"]


def test_png_zip_leaves_baked_pages_byte_identical_beside_lettered_ones():
    baked = {"page_number": 2, "image_bytes": placeholder_png(600, 849), "lettering_mode": "baked"}
    files, manifest, warnings = png_zip(page(), baked)
    assert not warnings
    assert files["pages/page-002.png"] == baked["image_bytes"]
    assert "lettering" not in manifest["pages"][1] and manifest["pages"][0]["lettering"] == "drawn"
