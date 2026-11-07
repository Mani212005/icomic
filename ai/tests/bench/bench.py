"""Hermetic, single-process performance baselines for the CPU and memory heavy image paths.

Run with ``make bench``. Every case is measured in a fresh subprocess so peak RSS is
not polluted by earlier cases; allocation counts come from one extra tracemalloc pass.
Inputs are synthesised from a fixed seed, so no artifacts, network or providers are used.
This module is not collected by pytest and is not part of the default CI run.
"""

from __future__ import annotations

import argparse
import hashlib
import json
import resource
import statistics
import subprocess
import sys
import time
import tracemalloc
from io import BytesIO
from pathlib import Path

AI_ROOT = Path(__file__).resolve().parents[2]
FIXTURES = AI_ROOT / "tests/fixtures/lettering"
sys.path.insert(0, str(AI_ROOT))

PAGE_SIZE = (1280, 1920)


def synthetic_page(index: int = 0, size: tuple[int, int] = PAGE_SIZE) -> bytes:
    """A deterministic painted-page stand-in: gradients, flat fills, edges and skin tones."""
    import numpy as np
    from PIL import Image, ImageDraw

    width, height = size
    rng = np.random.default_rng(1000 + index)
    yy, xx = np.mgrid[:height, :width].astype(np.float32)
    base = np.stack(
        [
            120 + 100 * np.sin(xx / 210.0 + index),
            130 + 90 * np.cos(yy / 330.0),
            140 + 80 * np.sin((xx + yy) / 400.0),
        ],
        axis=2,
    )
    base += rng.normal(0, 3, base.shape).astype(np.float32)
    image = Image.fromarray(np.clip(base, 0, 255).astype(np.uint8), "RGB")
    draw = ImageDraw.Draw(image)
    for panel in range(6):
        x0 = 40 + (panel % 2) * 620
        y0 = 40 + (panel // 2) * 620
        draw.rectangle((x0, y0, x0 + 580, y0 + 580), outline=(10, 10, 10), width=8)
        draw.ellipse((x0 + 150, y0 + 120, x0 + 430, y0 + 460), fill=(226, 168, 138))
        draw.rectangle((x0 + 60, y0 + 60, x0 + 200, y0 + 140), fill=(240, 232, 215))
        draw.line((x0, y0 + 300, x0 + 580, y0 + 280), fill=(20, 20, 30), width=5)
    output = BytesIO()
    image.save(output, format="PNG")
    return output.getvalue()


def lettering_document(utterances: int = 6):
    from app.gen.models import LetteringDocument

    document = json.loads((FIXTURES / "multiple-speakers.input.json").read_bytes())
    template = document["panels"][0]
    panel_count = max(1, utterances // 2)
    document["panels"] = []
    for number in range(panel_count):
        panel = json.loads(json.dumps(template))
        top = 0.02 + number * (0.96 / panel_count)
        height = 0.96 / panel_count - 0.02
        panel.update(panel_id=f"panel-{number}", order=number)
        panel["rect"] = {"x": 0.05, "y": top, "width": 0.9, "height": height}
        panel["reserved_zones"] = [
            {"x": 0.06, "y": top + 0.005, "width": 0.88, "height": height * 0.6}
        ]
        for utterance in panel["utterances"]:
            utterance["utterance_id"] = f"{utterance['utterance_id']}-{number}"
        document["panels"].append(panel)
    return LetteringDocument.model_validate(document)


def overlay_page(number: int, preset: str, image: bytes) -> dict:
    from app.services.lettering import layout_page

    document = lettering_document()
    document = document.model_copy(update={"page_id": f"bench-page-{number}"})
    layout = layout_page(document)
    state = {
        "version": "page-lettering-v1",
        "input": document.model_dump(mode="json"),
        "overrides": {},
        "zone_overrides": {},
        "layout": layout.model_dump(mode="json"),
    }
    return {
        "page_id": f"bench-page-{number}",
        "page_number": number,
        "lettering_mode": "overlay",
        "lettering": state,
        "finish_preset": preset,
        "image_bytes": image,
    }


def case_finish(strength: str):
    from app.services.finish_layer import finish_image

    data = synthetic_page()

    def run():
        return hashlib.sha256(finish_image(data, seed="bench", strength=strength)).hexdigest()[:12]

    return run


def case_lettering(pages: int):
    from app.services.lettering import layout_bytes, layout_page

    documents = [
        lettering_document().model_copy(update={"page_id": f"bench-{n}"}) for n in range(pages)
    ]

    def run():
        digest = hashlib.sha256()
        for document in documents:
            digest.update(layout_bytes(layout_page(document)))
        return digest.hexdigest()[:12]

    return run


def case_pdf(pages: int, preset: str):
    from app.services.pdf import PdfWriter

    images = [synthetic_page(n % 4) for n in range(pages)]

    def run():
        data = [overlay_page(n + 1, preset, images[n]) for n in range(pages)]
        pdf = PdfWriter().build_pdf("bench", "Bench Book", True, data)
        return f"{len(pdf)}"

    return run


CASES = {
    "finish-light": lambda: case_finish("light"),
    "finish-print": lambda: case_finish("print"),
    "finish-plus": lambda: case_finish("plus"),
    "finish-heavy": lambda: case_finish("heavy"),
    "lettering-1": lambda: case_lettering(1),
    "lettering-60": lambda: case_lettering(60),
    "pdf-13-off": lambda: case_pdf(13, "off"),
    "pdf-13-print": lambda: case_pdf(13, "print"),
    "pdf-60-off": lambda: case_pdf(60, "off"),
    "pdf-60-print": lambda: case_pdf(60, "print"),
}
DEFAULT_CASES = [name for name in CASES if name != "pdf-60-print"]


def child(name: str, trace: bool) -> None:
    run = CASES[name]()
    if trace:
        tracemalloc.start()
    wall, cpu = time.perf_counter(), time.process_time()
    fingerprint = run()
    wall, cpu = time.perf_counter() - wall, time.process_time() - cpu
    result = {"wall": wall, "cpu": cpu, "fingerprint": fingerprint}
    if trace:
        _, peak = tracemalloc.get_traced_memory()
        blocks = sum(stat.count for stat in tracemalloc.take_snapshot().statistics("filename"))
        result.update(traced_peak_mb=peak / 1e6, live_blocks=blocks)
    result["peak_rss_mb"] = resource.getrusage(resource.RUSAGE_SELF).ru_maxrss / 1024
    print(json.dumps(result))


def spawn(name: str, trace: bool = False) -> dict:
    command = [sys.executable, __file__, "--child", name] + (["--trace"] if trace else [])
    out = subprocess.run(command, check=True, capture_output=True, text=True, cwd=AI_ROOT)
    return json.loads(out.stdout.splitlines()[-1])


def summarise(values: list[float]) -> str:
    return f"{statistics.median(values):8.3f} [{min(values):.3f}-{max(values):.3f}]"


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("cases", nargs="*", help=f"any of: {', '.join(CASES)}")
    parser.add_argument("--runs", type=int, default=5)
    parser.add_argument("--trace", action="store_true", help="add a tracemalloc pass")
    parser.add_argument("--json", type=Path, help="write raw results here")
    parser.add_argument("--child")
    args = parser.parse_args()
    if args.child:
        child(args.child, args.trace)
        return
    names = args.cases or DEFAULT_CASES
    print(f"{'case':<14}{'wall s median [min-max]':>26}{'cpu s':>10}{'rss MB':>9}  fingerprint")
    report = {}
    for name in names:
        runs = [spawn(name) for _ in range(args.runs)]
        entry = {
            "wall": [r["wall"] for r in runs],
            "cpu": [r["cpu"] for r in runs],
            "peak_rss_mb": [r["peak_rss_mb"] for r in runs],
            "fingerprint": runs[0]["fingerprint"],
        }
        if args.trace:
            traced = spawn(name, trace=True)
            entry["traced_peak_mb"] = traced["traced_peak_mb"]
            entry["live_blocks"] = traced["live_blocks"]
        report[name] = entry
        extra = f"  traced {entry['traced_peak_mb']:.0f} MB" if args.trace else ""
        print(
            f"{name:<14}{summarise(entry['wall']):>26}{statistics.median(entry['cpu']):>10.3f}"
            f"{statistics.median(entry['peak_rss_mb']):>9.0f}  {entry['fingerprint']}{extra}",
            flush=True,
        )
    if args.json:
        args.json.write_text(json.dumps(report, indent=1))


if __name__ == "__main__":
    main()
