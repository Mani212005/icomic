#!/usr/bin/env python3
"""Review-only CLI: draw a serialized lettering layout over a copy of real art.

Run with the AI environment (Pillow/fontTools). It uses the same rasterizer as the PNG
export (ai/app/services/lettering/raster.py), so it does not wrap or shape text and never
alters the source art.
"""

import argparse
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "ai"))

from PIL import Image  # noqa: E402

from app.gen.models import LetteringLayout  # noqa: E402
from app.services.lettering.raster import draw_lettering  # noqa: E402


def render(source, layout, output):
    if source.resolve() == output.resolve():
        raise ValueError("Review output must differ from source art")
    with Image.open(source) as art:
        draw_lettering(art, LetteringLayout.model_validate(layout)).save(output)


if __name__ == "__main__":
    import json

    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("source", type=Path)
    parser.add_argument("layout", type=Path)
    parser.add_argument("output", type=Path)
    args = parser.parse_args()
    render(args.source, json.loads(args.layout.read_text()), args.output)
