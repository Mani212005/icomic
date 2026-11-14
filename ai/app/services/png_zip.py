"""Individual page PNG export: a ZIP of page PNGs, their provider sources and a manifest."""

import hashlib
import json
import zipfile
from io import BytesIO
from typing import Any

from PIL import Image

from ..adapters.meta_http import detect_image_media_type
from ..gen.models import LetteringLayout
from .images import IMAGE_EXTENSIONS
from .lettering.raster import draw_lettering

# Fixed timestamps keep the archive reproducible for the same inputs.
_ZIP_EPOCH = (2020, 1, 1, 0, 0, 0)

DISCLOSURE = (
    "AI-generation disclosure: this comic was created with Icomic AI using Meta generative "
    "models. All visual artwork, character artwork and panel compositions are AI-generated."
)


def _png(data: bytes, lettering: LetteringLayout | None = None) -> tuple[bytes, int, int]:
    """The page as PNG: PNG sources are kept byte-identical, other formats are converted.

    Stored overlay lettering is drawn over the artwork, so a lettered page is always re-encoded.
    """
    with Image.open(BytesIO(data)) as source:
        source.load()
        width, height = source.size
        if lettering is None and data.startswith(b"\x89PNG\r\n\x1a\n"):
            return data, width, height
        page = (
            draw_lettering(source, lettering)
            if lettering
            else source.convert("RGBA" if "A" in source.getbands() else "RGB")
        )
        converted = BytesIO()
        page.save(converted, format="PNG")
        return converted.getvalue(), width, height


def _extension(data: bytes) -> str:
    return IMAGE_EXTENSIONS.get(detect_image_media_type(data), "bin")


def build_png_zip(
    project_id: str,
    title: str,
    include_ai_disclosure: bool,
    pages_data: list[dict[str, Any]],
    sources_preserved: bool,
    layouts: list[LetteringLayout | None] | None = None,
) -> bytes:
    """``layouts`` holds, per page, the stored lettering to draw (None: nothing to draw)."""
    layouts = layouts or [None] * len(pages_data)
    files: list[tuple[str, bytes]] = []
    manifest_pages = []
    for index, page in enumerate(pages_data, start=1):
        stem = f"page-{index:03d}"
        layout = layouts[index - 1]
        png, width, height = _png(page["image_bytes"], layout)
        entry: dict[str, Any] = {
            "page_number": page["page_number"],
            "file": f"pages/{stem}.png",
            "width": width,
            "height": height,
            "sha256": hashlib.sha256(png).hexdigest(),
            "converted_from_source": png != page["source_bytes"],
        }
        files.append((entry["file"], png))
        # The provider's exact bytes are the provenance record; the PNG may be a conversion.
        source = page["source_bytes"]
        if source == png:
            entry["source_file"] = entry["file"]
        else:
            entry["source_file"] = f"sources/{stem}-source.{_extension(source)}"
            files.append((entry["source_file"], source))
        entry["source_sha256"] = hashlib.sha256(source).hexdigest()
        entry["source_verified"] = page["source_verified"]
        original = page.get("original_image_bytes")
        if original and original not in (png, source):
            entry["original_file"] = f"sources/{stem}-original.{_extension(original)}"
            files.append((entry["original_file"], original))
        if page.get("lettering_mode") == "overlay":
            # A withheld page carries the unlettered artwork; the export response warns about it.
            entry["lettering"] = "drawn" if layout else "withheld"
        entry["provenance"] = page["provenance_metadata"]
        manifest_pages.append(entry)

    manifest = {
        "project_id": project_id,
        "title": title,
        "format": "png_zip",
        "provenance_metadata_preserved": sources_preserved,
        "pages": manifest_pages,
    }
    files.append(("manifest.json", json.dumps(manifest, indent=2, sort_keys=True).encode()))
    if include_ai_disclosure:
        provenance = (
            "Original provider image files are included unmodified under sources/ or pages/. "
            "Icomic records this provenance but does not verify C2PA signatures."
            if sources_preserved
            else "Original provider image files could not all be included; provenance is incomplete."
        )
        files.append(("DISCLOSURE.txt", f"{title}\n\n{DISCLOSURE}\n{provenance}\n".encode()))

    output = BytesIO()
    with zipfile.ZipFile(output, "w", zipfile.ZIP_STORED) as archive:
        for name, data in files:
            archive.writestr(zipfile.ZipInfo(name, _ZIP_EPOCH), data)
    return output.getvalue()
