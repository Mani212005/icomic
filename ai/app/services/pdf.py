"""PDF assembly service for finished comic book export.

Generates a valid binary multi-page PDF embedding comic page images,
panel dialogue text, cover title, mandatory AI-generation disclosure,
and the provider's original image files attached unmodified.
"""

import hashlib
import json
import struct
import textwrap
import zlib
from datetime import datetime, timezone
from io import BytesIO
from typing import Any, ClassVar
from uuid import uuid4
from xml.sax.saxutils import escape

from PIL import Image

from ..adapters.storage.port import StoragePort
from ..gen.models import PageExportItem, PdfLetteringWarning
from .errors import ExportIncompleteError
from .finish_layer import FinishStrength, encode_finished, finish_pixels, page_seed
from .lettering.pdf import PdfLettering, eligibility
from .png_zip import build_png_zip


class UnsupportedImageMediaError(ValueError):
    """Raised when an image media type cannot be embedded in PDF without loss of provenance."""


def parse_jpeg_info(data: bytes) -> tuple[int, int, str]:
    """Parse JPEG dimensions (width, height) and color space without re-encoding.

    Extracts SOF segment parameters: precision, height, width, and number of components.
    """
    if not data.startswith(b"\xff\xd8"):
        raise ValueError("Not a valid JPEG binary: missing SOI signature")

    pos = 2
    length = len(data)
    while pos < length - 1:
        if data[pos] != 0xFF:
            pos += 1
            continue
        while pos < length and data[pos] == 0xFF:
            pos += 1
        if pos >= length:
            break
        marker = data[pos]
        pos += 1
        if marker in (0xD8, 0xD9):  # SOI, EOI
            continue
        if marker == 0xDA:  # SOS: Start of Scan (entropy-coded stream starts)
            break
        if pos + 2 > length:
            break
        seg_len = struct.unpack(">H", data[pos : pos + 2])[0]
        if seg_len < 2 or pos + seg_len > length:
            break
        # SOF markers: 0xC0 .. 0xCF except 0xC4 (DHT), 0xC8 (JPG), 0xCC (DAC)
        if 0xC0 <= marker <= 0xCF and marker not in (0xC4, 0xC8, 0xCC):
            height = struct.unpack(">H", data[pos + 3 : pos + 5])[0]
            width = struct.unpack(">H", data[pos + 5 : pos + 7])[0]
            components = data[pos + 7]
            cs = (
                "/DeviceGray"
                if components == 1
                else ("/DeviceRGB" if components == 3 else "/DeviceCMYK")
            )
            return width, height, cs
        pos += seg_len

    raise ValueError("Start of Frame (SOF) marker not found in JPEG data")


def escape_pdf_string(text: str) -> str:
    """Escape text for inclusion in PDF literal string parenthesis."""
    cleaned = text.encode("ascii", "replace").decode("ascii")
    cleaned = cleaned.replace("\\", "\\\\").replace("(", "\\(").replace(")", "\\)")
    return cleaned


def _wrap_cover_title(title: str, max_chars: int) -> list[str]:
    """Wrap a cover title into readable lines that fit the title column."""
    words = title.split()
    if not words:
        return [""]
    lines: list[str] = []
    current = ""
    for word in words:
        while len(word) > max_chars:
            if current:
                lines.append(current)
                current = ""
            lines.append(word[:max_chars])
            word = word[max_chars:]
        candidate = f"{current} {word}".strip()
        if len(candidate) > max_chars and current:
            lines.append(current)
            current = word
        else:
            current = candidate
    if current:
        lines.append(current)
    return lines


def parse_png_to_rgb(png_data: bytes) -> tuple[int, int, bytes]:
    """Parse a PNG image binary and extract width, height, and raw RGB byte stream.

    Decode indexed and truecolor PNGs consistently, compositing transparency onto
    the white page background used by the PDF image XObject.
    """
    if not png_data.startswith(b"\x89PNG\r\n\x1a\n"):
        raise ValueError("Invalid PNG header signature")
    with Image.open(BytesIO(png_data)) as image:
        rgba = image.convert("RGBA")
        rgb = Image.new("RGB", rgba.size, "white")
        rgb.paste(rgba, mask=rgba.getchannel("A"))
        width, height = rgb.size
        return width, height, rgb.tobytes()


def _display_image_payload(image_bytes: bytes) -> tuple[int, int, str, str, bytes]:
    """Return a compact PDF image stream while leaving source bytes untouched."""
    if image_bytes.startswith(b"\xff\xd8"):
        width, height, colorspace = parse_jpeg_info(image_bytes)
        return width, height, colorspace, "DCTDecode", image_bytes

    if image_bytes.startswith(b"\x89PNG\r\n\x1a\n") or (
        image_bytes.startswith(b"RIFF") and len(image_bytes) >= 12 and image_bytes[8:12] == b"WEBP"
    ):
        with Image.open(BytesIO(image_bytes)) as source:
            if source.mode in ("RGB", "L") and "transparency" not in source.info:
                rgb = source.convert("RGB")  # opaque: compositing on white is the identity
            else:
                rgba = source.convert("RGBA")
                rgb = Image.new("RGB", rgba.size, "white")
                rgb.paste(rgba, mask=rgba.getchannel("A"))
            width, height = rgb.size
            # Palette art and small flat illustrations retain their exact pixels.
            flat = source.mode in ("1", "P") or (rgb.getcolors(maxcolors=64) is not None)
            if flat:
                return width, height, "/DeviceRGB", "FlateDecode", zlib.compress(rgb.tobytes())
            encoded = BytesIO()
            rgb.save(encoded, format="JPEG", quality=91, optimize=True)
            return width, height, "/DeviceRGB", "DCTDecode", encoded.getvalue()

    raise UnsupportedImageMediaError("PDF export supports PNG, JPEG and WebP.")


def _image_extension(data: bytes) -> str | None:
    """File extension for the PNG, JPEG or WebP bytes an attachment carries, else None."""
    if data.startswith(b"\x89PNG\r\n\x1a\n"):
        return "png"
    if data.startswith(b"\xff\xd8"):
        return "jpeg"
    if data.startswith(b"RIFF") and data[8:12] == b"WEBP":
        return "webp"
    return None


def _finished_display_jpeg(image_bytes: bytes, seed: int, strength: FinishStrength) -> bytes:
    """Finish a page and encode it as the JPEG display copy embedded in the PDF."""
    finished = finish_pixels(image_bytes, seed=seed, strength=strength)
    if finished is None:
        image = _decode(image_bytes)
    else:
        image, image_format = finished
        if image_format.upper() != "PNG":
            # Lossy source formats are re-encoded by the finisher; keep that generation loss.
            image = _decode(encode_finished(image, image_format))
    display_output = BytesIO()
    image.convert("RGB").save(display_output, format="JPEG", quality=92, optimize=True)
    return display_output.getvalue()


def _decode(data: bytes) -> Image.Image:
    with Image.open(BytesIO(data)) as source:
        source.load()
        return source.copy()


def _image_object(
    width: int, height: int, colorspace: str, filter_name: str, payload: bytes
) -> bytes:
    return (
        f"<< /Type /XObject /Subtype /Image /Width {width} /Height {height} "
        f"/ColorSpace {colorspace} /BitsPerComponent 8 /Filter /{filter_name} "
        f"/Length {len(payload)} >>\nstream\n".encode()
        + payload
        + b"\nendstream"
    )


class PdfWriter:
    """Pure-Python minimal PDF 1.4 binary document writer with standard fonts and images."""

    PAGE_DIMENSIONS: ClassVar[dict[str, tuple[float, float]]] = {
        "A4": (595.28, 841.89),
        "US_Letter": (612.0, 792.0),
        "Custom_Comic": (477.0, 738.0),  # 6.625 x 10.25 in, the size the export dialog names
    }

    def __init__(self, page_size: str = "A4", storage_adapter: StoragePort | None = None):
        if page_size not in self.PAGE_DIMENSIONS:
            raise ValueError(f"Unsupported page size: {page_size}")
        self.width, self.height = self.PAGE_DIMENSIONS[page_size]
        self.page_size_name = page_size
        self.storage_adapter = storage_adapter
        self.objects: list[bytes] = []

    def _reserve_id(self) -> int:
        self.objects.append(b"")
        return len(self.objects)

    def _add_object(self, data: bytes) -> int:
        self.objects.append(data)
        return len(self.objects)

    def _set_object(self, obj_id: int, data: bytes) -> None:
        self.objects[obj_id - 1] = data

    def build_pdf(
        self,
        project_id: str,
        title: str,
        include_ai_disclosure: bool,
        pages_data: list[dict[str, Any]],
        sources_preserved: bool = False,
    ) -> bytes:
        """Assemble all document pages, embedded images, metadata, and cross-reference table."""
        for idx, page in enumerate(pages_data, start=1):
            image_bytes = page.get("image_bytes")
            if not image_bytes:
                raise ExportIncompleteError(
                    f"Page {page.get('page_number', idx)} has no stored image to export."
                )
            # The provider's untouched file is the provenance source, whatever display copy follows.
            if not page.get("source_bytes"):
                page["source_bytes"] = image_bytes
            preset = page.get("finish_preset", "off")
            eligible = page.get("lettering_mode") == "overlay" or page.get(
                "finish_preset_explicit", False
            )
            strength = {
                "print": FinishStrength.PRINT,
                "print-plus": FinishStrength.PLUS,
                "print-heavy": FinishStrength.HEAVY,
            }.get(preset)
            if eligible and strength is not None:
                page_identity = (
                    page.get("page_id") or f"{project_id}/page-{page.get('page_number', idx)}"
                )
                display_bytes = _finished_display_jpeg(
                    image_bytes, page_seed(f"{page_identity}/{preset}"), strength
                )
                derivative_hash = hashlib.sha256(display_bytes).hexdigest()
                derivative_key = f"finishes/{page_identity}/{preset}/{derivative_hash}.jpg"
                if self.storage_adapter is not None:
                    self.storage_adapter.put(derivative_key, display_bytes, "image/jpeg")
                    page["finished_image_url"] = derivative_key
                if not page.get("original_image_bytes"):
                    page["original_image_bytes"] = image_bytes
                page["image_bytes"] = display_bytes

        checked = [eligibility(page) for page in pages_data]
        self.warnings = [warning for _, warning in checked if warning]
        catalog_id = self._reserve_id()
        pages_root_id = self._reserve_id()

        # Standard 14 Type1 Fonts
        font_f1 = self._add_object(
            b"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica /Encoding /WinAnsiEncoding >>"
        )
        font_f2 = self._add_object(
            b"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica-Bold /Encoding /WinAnsiEncoding >>"
        )
        font_f3 = self._add_object(
            b"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica-Oblique /Encoding /WinAnsiEncoding >>"
        )
        font_res_str = f"<< /F1 {font_f1} 0 R /F2 {font_f2} 0 R /F3 {font_f3} 0 R >>"

        page_ids: list[int] = []
        source_files: list[str] = []

        # -----------------------------------------------------------------
        # Page 1: Cover Page
        # -----------------------------------------------------------------
        cover_page_id = self._reserve_id()
        cover_lines: list[bytes] = []

        # Compact masthead, illustration, disclosure, and provenance layout.
        cover_lines.extend(
            [
                b"0.12 0.18 0.32 rg",
                f"0 {self.height - 82:.2f} {self.width:.2f} 82 re f".encode(),
                b"q 0.84 0.76 0.61 rg",
                f"28 {self.height - 75:.2f} m 68 {self.height - 76:.2f} l 71 {self.height - 39:.2f} l 31 {self.height - 37:.2f} l h f".encode(),
                b"1 1 1 RG 1.5 w",
                f"25 {self.height - 73:.2f} m 64 {self.height - 72:.2f} l 66 {self.height - 37:.2f} l 27 {self.height - 38:.2f} l h S".encode(),
                b"0.77 0.50 0.44 rg",
                f"29 {self.height - 56:.2f} m 44 {self.height - 55:.2f} l 45 {self.height - 43:.2f} l 30 {self.height - 42:.2f} l h f 47 {self.height - 55:.2f} m 61 {self.height - 55:.2f} l 62 {self.height - 44:.2f} l 48 {self.height - 43:.2f} l h f".encode(),
                b"0.98 0.96 0.92 rg 1 1 1 RG 1 w",
                f"31 {self.height - 70:.2f} m 62 {self.height - 69:.2f} l 62 {self.height - 60:.2f} l 31 {self.height - 61:.2f} l h B".encode(),
                b"0.77 0.50 0.44 RG 2.5 w 1 J",
                f"31 {self.height - 67.1:.2f} m 27 {self.height - 69.4:.2f} 22.5 {self.height - 70.8:.2f} 17 {self.height - 71.3:.2f} c S".encode(),
                b"0.29 0.27 0.26 rg 1 1 1 RG 2 w 0 J",
                f"30.5 {self.height - 62.9:.2f} m 45.3 {self.height - 44:.2f} l 50.2 {self.height - 38.3:.2f} l 55.1 {self.height - 43.2:.2f} l 41.2 {self.height - 63.7:.2f} l 31.4 {self.height - 67.8:.2f} l h B".encode(),
                b"0.84 0.76 0.61 rg 0.2 0.2 0.2 RG 0.8 w",
                f"30.5 {self.height - 62.9:.2f} m 28.9 {self.height - 67.8:.2f} l 41.2 {self.height - 63.7:.2f} l h B 29.7 {self.height - 67:.2f} m 36.3 {self.height - 64.5:.2f} l S Q".encode(),
                b"BT /F2 14 Tf 1.0 1.0 1.0 rg",
                f"86 {self.height - 49:.2f} Td (ICOMIC GRAPHIC NOVEL SERIES) Tj ET".encode(),
            ]
        )
        esc_title = escape_pdf_string(title)
        title_max_chars = max(18, int((self.width - 100) / 14))
        title_lines = _wrap_cover_title(title, title_max_chars)
        title_y = self.height - 126
        for line_number, title_line in enumerate(title_lines):
            cover_lines.append(b"BT /F2 26 Tf 0.1 0.1 0.15 rg")
            cover_lines.append(
                f"50 {title_y - line_number * 30:.2f} Td ({escape_pdf_string(title_line)}) Tj ET".encode()
            )
        subtitle_y = title_y - len(title_lines) * 30 - 3
        cover_lines.append(b"BT /F3 12 Tf 0.35 0.35 0.4 rg")
        cover_lines.append(f"50 {subtitle_y:.2f} Td (AI-Generated Story & Artwork) Tj ET".encode())

        disclosure_y = 177
        disclosure_h = 112 if include_ai_disclosure else 0
        provenance_y = 153
        image_top = subtitle_y - 19
        image_bottom = disclosure_y + disclosure_h + 31
        image_max_h = max(0, min(305, image_top - image_bottom))
        cover_image = pages_data[0].get("image_bytes") if pages_data else None
        cover_source = cover_image
        cover_payload: tuple[int, int, str, str, bytes] | None = None
        if cover_image and image_max_h >= 72:
            try:
                cover_payload = _display_image_payload(cover_image)
                img_w, img_h, colorspace, filter_name, payload = cover_payload
            except (OSError, ValueError, struct.error, zlib.error):
                cover_image = None
            else:
                cover_image_id = self._add_object(
                    _image_object(img_w, img_h, colorspace, filter_name, payload)
                )
                scale = min((self.width - 150) / img_w, image_max_h / img_h)
                display_w, display_h = img_w * scale, img_h * scale
                image_x = (self.width - display_w) / 2
                image_y = image_top - display_h
                cover_lines.extend(
                    [
                        b"0.75 0.78 0.82 RG 0.8 w",
                        f"{image_x - 1:.2f} {image_y - 1:.2f} {display_w + 2:.2f} {display_h + 2:.2f} re S".encode(),
                        b"q",
                        f"{display_w:.2f} 0 0 {display_h:.2f} {image_x:.2f} {image_y:.2f} cm /CoverIm Do Q".encode(),
                    ]
                )
        if not cover_image or image_max_h < 72:
            placeholder_y = max(image_bottom, image_top - 130)
            cover_lines.extend(
                [
                    b"0.94 0.96 0.98 rg 0.78 0.81 0.85 RG 0.8 w",
                    f"90 {placeholder_y:.2f} {self.width - 180:.2f} 110 re B".encode(),
                    b"BT /F3 12 Tf 0.45 0.48 0.52 rg",
                    f"{self.width / 2 - 72:.2f} {placeholder_y + 52:.2f} Td (Artwork preview unavailable) Tj ET".encode(),
                ]
            )

        if include_ai_disclosure:
            box_w = self.width - 100
            cover_lines.extend(
                [
                    b"0.94 0.96 0.98 rg 0.25 0.35 0.55 RG 1.2 w",
                    f"50 {disclosure_y:.2f} {box_w:.2f} {disclosure_h:.2f} re B".encode(),
                    b"BT /F2 10 Tf 0.15 0.25 0.45 rg",
                    f"64 {disclosure_y + disclosure_h - 20:.2f} Td (MANDATORY AI-GENERATION DISCLOSURE) Tj ET".encode(),
                ]
            )
            disclosure_statements = [
                "Notice: This comic book was created with Icomic AI using Meta generative models.",
                "All visual artwork, character artwork, and panel compositions are AI-generated.",
                *(
                    [
                        "Original Meta image files, with any embedded provenance metadata, are attached unmodified.",
                        "Icomic records this provenance but does not verify C2PA signatures.",
                    ]
                    if sources_preserved
                    else [
                        "Original Meta image files could not all be attached; provenance is incomplete.",
                    ]
                ),
            ]
            for row, statement in enumerate(disclosure_statements):
                cover_lines.append(
                    f"BT /F1 7.5 Tf 0.2 0.2 0.25 rg 64 {disclosure_y + disclosure_h - 38 - row * 15:.2f} Td ({escape_pdf_string(statement)}) Tj ET".encode()
                )

        cover_lines.append(b"BT /F2 9 Tf 0.15 0.15 0.2 rg")
        cover_lines.append(f"50 {provenance_y:.2f} Td (PROJECT & PROVENANCE RECORD) Tj ET".encode())
        spec_details = [
            f"Project Identifier: {project_id}",
            f"Total Comic Pages: {len(pages_data)}",
            f"Page Dimensions: {self.width:.0f} x {self.height:.0f} points ({self.page_size_name})",
            "Provenance: original source files attached unmodified"
            if sources_preserved
            else "Provenance: incomplete, original source files missing",
            f"Assembled: {datetime.now(timezone.utc).strftime('%Y-%m-%d %H:%M:%SZ')}",
        ]
        for row, detail in enumerate(spec_details):
            cover_lines.append(
                f"BT /F1 7 Tf 0.3 0.3 0.3 rg 50 {provenance_y - 14 - row * 12:.2f} Td ({escape_pdf_string(detail)}) Tj ET".encode()
            )

        # Cover Footer
        cover_lines.append(b"BT /F1 8 Tf 0.5 0.5 0.5 rg")
        cover_lines.append(
            b"50 25 Td (Icomic AI Publishing Pipeline | Powered by Meta AI Models | Cover Page) Tj ET"
        )

        cover_bytes = b"\n".join(cover_lines)
        cover_comp = zlib.compress(cover_bytes)
        cover_content_id = self._add_object(
            f"<< /Length {len(cover_comp)} /Filter /FlateDecode >>\nstream\n".encode()
            + cover_comp
            + b"\nendstream"
        )

        self._set_object(
            cover_page_id,
            f"<< /Type /Page /Parent {pages_root_id} 0 R /MediaBox [0 0 {self.width:.2f} {self.height:.2f}] "
            f"/Resources << /Font {font_res_str} /XObject << /CoverIm {cover_image_id} 0 R >> >> /Contents {cover_content_id} 0 R >>".encode()
            if cover_image and image_max_h >= 72
            else f"/Resources << /Font {font_res_str} >> /Contents {cover_content_id} 0 R >>".encode(),
        )
        page_ids.append(cover_page_id)

        # -----------------------------------------------------------------
        # Comic Pages (Pages 2 .. N+1)
        # -----------------------------------------------------------------
        total_pdf_pages = len(pages_data) + 1

        lettering = PdfLettering(self._add_object, [layout for layout, _ in checked if layout])
        for idx, p in enumerate(pages_data, start=1):
            layout, warning = checked[idx - 1]
            overlay = p.get("lettering_mode") == "overlay"
            page_id = self._reserve_id()
            page_lines: list[bytes] = []

            # 1. Page Header
            esc_hdr = escape_pdf_string(f"{title} - Page {p.get('page_number', idx)}")
            header_font_size = min(10.0, max(6.0, (self.width - 100) / (len(esc_hdr) * 0.55)))
            page_lines.append(f"BT /F2 {header_font_size:.2f} Tf 0.2 0.2 0.2 rg".encode())
            page_lines.append(f"50 {self.height - 40:.2f} Td ({esc_hdr}) Tj ET".encode())

            page_lines.append(b"0.7 0.7 0.7 RG 0.5 w")
            page_lines.append(
                f"50 {self.height - 46:.2f} m {self.width - 50:.2f} {self.height - 46:.2f} l S".encode()
            )

            # 2. Embedded Page Image XObject
            img_bytes = p["image_bytes"]

            try:
                if cover_payload is not None and img_bytes is cover_source:
                    img_w, img_h, colorspace, filter_name, payload = cover_payload
                else:
                    img_w, img_h, colorspace, filter_name, payload = _display_image_payload(
                        img_bytes
                    )
            except OSError as exc:
                if img_bytes.startswith(b"RIFF") and img_bytes[8:12] == b"WEBP":
                    raise
                page_num = p.get("page_number", idx)
                raise UnsupportedImageMediaError(
                    f"Invalid or unsupported image on page {page_num}."
                ) from exc
            except (KeyError, ValueError, struct.error, zlib.error) as exc:
                page_num = p.get("page_number", idx)
                raise UnsupportedImageMediaError(
                    f"Invalid or unsupported image on page {page_num}."
                ) from exc
            img_xobj_id = self._add_object(
                _image_object(img_w, img_h, colorspace, filter_name, payload)
            )

            # Attach the provider's exact bytes unless a JPEG is embedded verbatim as the display.
            source_bytes = p["source_bytes"]
            if not (source_bytes == img_bytes and source_bytes.startswith(b"\xff\xd8")):
                extension = _image_extension(source_bytes)
                subtype = f"image#2F{extension}" if extension else "application#2Foctet-stream"
                extension = extension or "bin"
                source_id = self._add_object(
                    f"<< /Type /EmbeddedFile /Subtype /{subtype} /Length {len(source_bytes)} >>\nstream\n".encode()
                    + source_bytes
                    + b"\nendstream"
                )
                filename = f"page-{idx}-original.{extension}"
                file_id = self._add_object(
                    f"<< /Type /Filespec /F ({filename}) /EF << /F {source_id} 0 R >> >>".encode()
                )
                source_files.append(f"({filename}) {file_id} 0 R")

            original_image_bytes = p.get("original_image_bytes")
            if (
                original_image_bytes
                and original_image_bytes != img_bytes
                and original_image_bytes != source_bytes
            ):
                original_extension = _image_extension(original_image_bytes)
                if original_extension is None:
                    raise UnsupportedImageMediaError(
                        f"Unsupported original line art image format on page {p.get('page_number', idx)}."
                    )
                original_id = self._add_object(
                    f"<< /Type /EmbeddedFile /Subtype /image#2F{original_extension} "
                    f"/Length {len(original_image_bytes)} >>\nstream\n".encode()
                    + original_image_bytes
                    + b"\nendstream"
                )
                original_filename = f"page-{idx}-original-line-art.{original_extension}"
                original_file_id = self._add_object(
                    f"<< /Type /Filespec /F ({original_filename}) "
                    f"/EF << /F {original_id} 0 R >> >>".encode()
                )
                source_files.append(f"({original_filename}) {original_file_id} 0 R")

            panels = [] if overlay else p.get("panels", [])
            dialogue_text = None if overlay else p.get("dialogue_text")
            script_rows = 0
            panel_gaps = 0
            if panels:
                for panel in panels:
                    narr = panel.get("narration", "").strip()
                    dial = panel.get("dialogue", "").strip()
                    scene = panel.get("scene_description", "").strip()
                    script_rows += (
                        1 + bool(narr) + bool(dial) + bool(not narr and not dial and scene)
                    )
                    panel_gaps += 1
            elif dialogue_text:
                script_rows = len(dialogue_text.splitlines())

            has_script = bool(script_rows)
            script_height = 55 + script_rows * 13 + panel_gaps * 4 if has_script else 0
            note_lines = []
            if warning:
                label = "LETTERING REVIEW REQUIRED" if layout else "LETTERING NOT EXPORTED"
                note = f"{label} - {warning.code}: {warning.message}"
                note_lines = textwrap.wrap(note, width=max(18, int((self.width - 100) / 4.8)))
                script_height = max(45, len(note_lines) * 10 + 15)

            # Fit artwork into the page area left by the actual transcript and footer.
            avail_w = self.width - 80
            avail_h = max(80, self.height - 58 - 60 - script_height)
            scale = min(avail_w / img_w, avail_h / img_h)
            display_w = img_w * scale
            display_h = img_h * scale
            img_x = (self.width - display_w) / 2.0
            img_y = self.height - 58 - display_h

            # Border around comic artwork
            page_lines.append(b"0.75 0.75 0.75 RG 1 w")
            page_lines.append(
                f"{img_x - 1:.2f} {img_y - 1:.2f} {display_w + 2:.2f} {display_h + 2:.2f} re S".encode()
            )

            # Draw Image
            page_lines.append(b"q")
            page_lines.append(
                f"{display_w:.2f} 0 0 {display_h:.2f} {img_x:.2f} {img_y:.2f} cm /Im1 Do Q".encode()
            )

            if layout:
                page_lines.append(
                    lettering.draw(
                        layout,
                        round(img_x, 2),
                        round(img_y, 2),
                        round(display_w, 2),
                        round(display_h, 2),
                    )
                )
            if warning:
                for row, line in enumerate(note_lines):
                    page_lines.append(
                        f"BT /F2 8 Tf 0.65 0.1 0.1 rg 50 {img_y - 13 - row * 10:.2f} Td ({escape_pdf_string(line)}) Tj ET".encode()
                    )

            # 3. Panel Dialogue and Narration Section
            text_start_y = img_y - 24
            if has_script:
                page_lines.append(b"BT /F2 10 Tf 0.15 0.2 0.35 rg")
                page_lines.append(
                    f"50 {text_start_y:.2f} Td (PANEL SCRIPT & DIALOGUE) Tj ET".encode()
                )

            curr_y = text_start_y - 18

            if panels:
                for panel in panels:
                    pnum = panel.get("panel_number", 1)
                    dial = panel.get("dialogue", "").strip()
                    narr = panel.get("narration", "").strip()
                    scene = panel.get("scene_description", "").strip()

                    page_lines.append(b"BT /F2 9 Tf 0.2 0.3 0.5 rg")
                    page_lines.append(f"55 {curr_y:.2f} Td (Panel {pnum}:) Tj ET".encode())
                    curr_y -= 13

                    if narr:
                        page_lines.append(b"BT /F3 9 Tf 0.3 0.3 0.3 rg")
                        esc_n = escape_pdf_string(f"[Narration] {narr}")
                        page_lines.append(f"65 {curr_y:.2f} Td ({esc_n}) Tj ET".encode())
                        curr_y -= 13
                    if dial:
                        page_lines.append(b"BT /F1 9 Tf 0.1 0.1 0.1 rg")
                        esc_d = escape_pdf_string(f'"{dial}"')
                        page_lines.append(f"65 {curr_y:.2f} Td ({esc_d}) Tj ET".encode())
                        curr_y -= 13
                    elif not narr and scene:
                        page_lines.append(b"BT /F3 9 Tf 0.4 0.4 0.4 rg")
                        esc_s = escape_pdf_string(f"(Scene: {scene})")
                        page_lines.append(f"65 {curr_y:.2f} Td ({esc_s}) Tj ET".encode())
                        curr_y -= 13
                    curr_y -= 4
            elif dialogue_text:
                for line in dialogue_text.splitlines():
                    page_lines.append(b"BT /F1 9 Tf 0.1 0.1 0.1 rg")
                    esc_l = escape_pdf_string(line)
                    page_lines.append(f"60 {curr_y:.2f} Td ({esc_l}) Tj ET".encode())
                    curr_y -= 13
            elif has_script:
                page_lines.append(b"BT /F3 9 Tf 0.4 0.4 0.4 rg")
                page_lines.append(
                    f"60 {curr_y:.2f} Td (Visual storytelling panel - dialogue integrated in artwork.) Tj ET".encode()
                )

            # 4. Page Footer
            page_lines.append(b"0.8 0.8 0.8 RG 0.5 w")
            page_lines.append(f"50 42 m {self.width - 50:.2f} 42 l S".encode())

            page_lines.append(b"BT /F1 8 Tf 0.4 0.4 0.4 rg")
            if include_ai_disclosure:
                page_lines.append(
                    b"50 28 Td (AI-Generated Content | Original Sources Attached | Icomic AI) Tj ET"
                    if sources_preserved
                    else b"50 28 Td (AI-Generated Content | Provenance Incomplete | Icomic AI) Tj ET"
                )
            else:
                page_lines.append(b"50 28 Td (Icomic AI Graphic Novel) Tj ET")

            page_num_str = f"Page {idx + 1} of {total_pdf_pages}"
            esc_pn = escape_pdf_string(page_num_str)
            page_lines.append(b"BT /F1 8 Tf 0.4 0.4 0.4 rg")
            page_lines.append(f"{self.width - 120:.2f} 28 Td ({esc_pn}) Tj ET".encode())

            page_bytes = b"\n".join(page_lines)
            page_comp = zlib.compress(page_bytes)
            page_content_id = self._add_object(
                f"<< /Length {len(page_comp)} /Filter /FlateDecode >>\nstream\n".encode()
                + page_comp
                + b"\nendstream"
            )

            page_fonts = font_res_str
            if layout:
                page_fonts = font_res_str[:-2] + lettering.resources + " >>"
            res_dict = f"<< /Font {page_fonts} /XObject << /Im1 {img_xobj_id} 0 R >> >>"
            self._set_object(
                page_id,
                f"<< /Type /Page /Parent {pages_root_id} 0 R /MediaBox [0 0 {self.width:.2f} {self.height:.2f}] "
                f"/Resources {res_dict} /Contents {page_content_id} 0 R >>".encode(),
            )
            page_ids.append(page_id)

        # -----------------------------------------------------------------
        # Pages Tree Object
        # -----------------------------------------------------------------
        kids_str = " ".join(f"{pid} 0 R" for pid in page_ids)
        self._set_object(
            pages_root_id,
            f"<< /Type /Pages /Kids [ {kids_str} ] /Count {len(page_ids)} >>".encode(),
        )

        # -----------------------------------------------------------------
        # Document Metadata Stream (XMP with recorded provenance)
        # -----------------------------------------------------------------
        provenance_records = [
            {
                "page_number": p.get("page_number", i),
                "image_url": p.get("image_url", ""),
                "original_image_url": p.get("original_image_url", ""),
                "provenance": p.get("provenance_metadata", {}),
                "source_sha256": hashlib.sha256(p["source_bytes"]).hexdigest(),
            }
            for i, p in enumerate(pages_data, start=1)
        ]
        xmp_xml = (
            f'<?xpacket begin="" id="W5M0MpCehiHzreSzNTczkc9d"?>\n'
            f'<x:xmpmeta xmlns:x="adobe:ns:meta/">\n'
            f' <rdf:RDF xmlns:rdf="http://www.w3.org/1999/02/22-rdf-syntax-ns#">\n'
            f'  <rdf:Description rdf:about="" xmlns:dc="http://purl.org/dc/elements/1.1/">\n'
            f'   <dc:title><rdf:Alt><rdf:li xml:lang="x-default">{escape(title)}</rdf:li></rdf:Alt></dc:title>\n'
            f"   <dc:creator><rdf:Seq><rdf:li>Icomic AI</rdf:li></rdf:Seq></dc:creator>\n"
            f'   <dc:description><rdf:Alt><rdf:li xml:lang="x-default">'
            f"AI-Generated Comic Book"
            f"</rdf:li></rdf:Alt></dc:description>\n"
            f"  </rdf:Description>\n"
            f'  <rdf:Description rdf:about="" xmlns:icomic="https://icomic.app/schema/provenance/1.0">\n'
            f"   <icomic:projectId>{escape(project_id)}</icomic:projectId>\n"
            f"   <icomic:provenanceRecords>{escape(json.dumps(provenance_records))}</icomic:provenanceRecords>\n"
            f"   <icomic:aiDisclosure>{include_ai_disclosure!s}</icomic:aiDisclosure>\n"
            f"   <icomic:sourcesPreserved>{sources_preserved!s}</icomic:sourcesPreserved>\n"
            f"  </rdf:Description>\n"
            f" </rdf:RDF>\n"
            f"</x:xmpmeta>\n"
            f'<?xpacket end="w"?>'
        ).encode()

        metadata_id = self._add_object(
            f"<< /Type /Metadata /Subtype /XML /Length {len(xmp_xml)} >>\nstream\n".encode()
            + xmp_xml
            + b"\nendstream"
        )

        # Catalog Object
        attachments = (
            f" /Names << /EmbeddedFiles << /Names [{' '.join(sorted(source_files))}] >> >>"
            if source_files
            else ""
        )
        self._set_object(
            catalog_id,
            f"<< /Type /Catalog /Pages {pages_root_id} 0 R /Metadata {metadata_id} 0 R{attachments} >>".encode(),
        )

        # Document Info Dictionary
        now_dt = datetime.now(timezone.utc).strftime("%Y%m%d%H%M%SZ")
        info_id = self._add_object(
            f"<< /Title ({esc_title}) /Author (Icomic AI) /Creator (Icomic AI Service) "
            f"/Subject (AI-Generated Comic Book) "
            f"/CreationDate (D:{now_dt}) /ModDate (D:{now_dt}) >>".encode()
        )

        # -----------------------------------------------------------------
        # Binary Assembly with Cross-Reference Table
        # -----------------------------------------------------------------
        parts = [b"%PDF-1.4\n%\xe2\xe3\xcf\xd3\n"]
        offset = len(parts[0])

        offsets = [0]
        for idx, obj in enumerate(self.objects, start=1):
            offsets.append(offset)
            framed = (f"{idx} 0 obj\n".encode(), obj, b"\nendobj\n")
            parts.extend(framed)
            offset += sum(len(part) for part in framed)

        xref = [f"xref\n0 {len(self.objects) + 1}\n".encode(), b"0000000000 65535 f \r\n"]
        xref.extend(f"{off:010d} 00000 n \r\n".encode() for off in offsets[1:])
        xref.append(
            f"trailer\n<< /Size {len(self.objects) + 1} /Root {catalog_id} 0 R /Info {info_id} 0 R >>\n"
            f"startxref\n{offset}\n%%EOF\n".encode()
        )
        # One join sizes the result exactly; a growing bytearray plus bytes() holds two copies.
        return b"".join([*parts, *xref])


class CollectedPages:
    """Page art and provenance sources fetched once for either export format."""

    def __init__(self, pages_data: list[dict[str, Any]]):
        self.pages_data = pages_data
        # True only when every AI-generated source file is attached byte-identical to what the
        # provider returned; an unverifiable or missing source never counts as preserved.
        self.sources_preserved = all(page["source_verified"] for page in pages_data)


class PdfAssemblyService:
    """Service compiling comic book pages into an assembled PDF with provenance and disclosure."""

    def __init__(self, storage_adapter: StoragePort):
        self.storage_adapter = storage_adapter

    def _fetch(self, url: str, what: str, page_number: int) -> bytes:
        try:
            data = self.storage_adapter.get(url)
        except (KeyError, ValueError, OSError) as exc:
            raise ExportIncompleteError(
                f"Page {page_number}: {what} is no longer in storage. Regenerate it and export again."
            ) from exc
        if not data:
            raise ExportIncompleteError(f"Page {page_number}: {what} is empty.")
        return data

    def _provider_source(
        self, url: str, data: bytes, provenance: dict[str, Any], page_number: int
    ) -> tuple[bytes, bool]:
        """The provider's untouched file for a stored artifact, and whether its hash is on record.

        Page cleanup stores a derivative beside the raw file; the raw file carries the
        provider's embedded provenance, so it is the source whenever one exists.
        """
        raw_url = provenance.get("raw_image_url")
        source = (
            self._fetch(raw_url, "its unmodified provider image", page_number) if raw_url else data
        )
        recorded = provenance.get("source_hash")
        return source, bool(recorded) and hashlib.sha256(source).hexdigest() == recorded

    def collect_pages(
        self,
        pages: list[PageExportItem],
        finish_preset: str = "off",
        finish_preset_explicit: bool = False,
    ) -> CollectedPages:
        if not pages:
            raise ValueError("Cannot assemble an export with zero pages.")

        pages_data: list[dict[str, Any]] = []
        for p in pages:
            if not p.image_url:
                raise ExportIncompleteError(f"Page {p.page_number} has no image to export.")
            img_bytes = self._fetch(p.image_url, "its page image", p.page_number)
            original_image_bytes: bytes | None = None
            original_image_url = getattr(p, "original_image_url", None)
            if original_image_url:
                original_image_bytes = self._fetch(
                    original_image_url, "its original image", p.page_number
                )
            provenance = p.provenance_metadata or {}
            if p.ai_generated:
                source_bytes, source_verified = self._provider_source(
                    p.image_url, img_bytes, provenance, p.page_number
                )
            elif original_image_bytes is not None:
                # A user-coloured page: the preserved AI source is the line art underneath it.
                source_bytes, source_verified = self._provider_source(
                    original_image_url,
                    original_image_bytes,
                    p.original_provenance_metadata or {},
                    p.page_number,
                )
            else:
                source_bytes, source_verified = img_bytes, False
            page_preset = getattr(p.finish_preset, "root", p.finish_preset)
            page_finish_explicit = finish_preset_explicit or p.finish_preset_explicit
            if not page_preset or page_preset == "off":
                page_preset = getattr(finish_preset, "root", finish_preset) or "off"

            # Convert p.panels into list of dicts if present
            panels_list: list[dict[str, Any]] = []
            if getattr(p, "panels", None):
                for pnl in p.panels:
                    if hasattr(pnl, "model_dump"):
                        panels_list.append(pnl.model_dump())
                    elif isinstance(pnl, dict):
                        panels_list.append(pnl)

            pages_data.append(
                {
                    "page_id": p.page_id,
                    "lettering_mode": p.lettering_mode,
                    "lettering": p.lettering.model_dump(mode="json") if p.lettering else None,
                    "lettering_stale": p.lettering_stale,
                    "page_number": p.page_number,
                    "finish_preset": str(page_preset),
                    "finish_preset_explicit": page_finish_explicit,
                    "image_url": p.image_url,
                    "image_bytes": img_bytes,
                    "original_image_url": original_image_url,
                    "original_image_bytes": original_image_bytes,
                    "source_bytes": source_bytes,
                    "source_verified": source_verified,
                    "provenance_metadata": provenance,
                    "panels": panels_list,
                    "dialogue_text": getattr(p, "dialogue_text", None),
                }
            )
        return CollectedPages(pages_data)

    def assemble_pdf(
        self,
        project_id: str,
        title: str,
        page_size: str,
        include_ai_disclosure: bool,
        pages: list[PageExportItem],
        finish_preset: str = "off",
        finish_preset_explicit: bool = False,
    ) -> tuple[str, bool, int, list[PdfLetteringWarning]]:
        """Assemble pages into PDF blob, upload to storage, and return download URL."""
        collected = self.collect_pages(pages, finish_preset, finish_preset_explicit)
        writer = PdfWriter(page_size=page_size, storage_adapter=self.storage_adapter)
        pdf_content = writer.build_pdf(
            project_id=project_id,
            title=title,
            include_ai_disclosure=include_ai_disclosure,
            pages_data=collected.pages_data,
            sources_preserved=collected.sources_preserved,
        )

        # Page count: 1 cover page + N comic pages
        total_page_count = len(collected.pages_data) + 1

        key = f"exports/{project_id}/{uuid4().hex}/comic.pdf"
        url = self.storage_adapter.put(key, pdf_content, "application/pdf")
        return url, collected.sources_preserved, total_page_count, writer.warnings

    def assemble_png_zip(
        self,
        project_id: str,
        title: str,
        include_ai_disclosure: bool,
        pages: list[PageExportItem],
    ) -> tuple[str, bool, int, list[PdfLetteringWarning]]:
        """Bundle each page as a PNG with its provider source files and a provenance manifest."""
        collected = self.collect_pages(pages)
        # The same stored layout and review gate as the PDF: stale or flagged lettering is not
        # drawn and surfaces as a warning instead.
        checked = [eligibility(page) for page in collected.pages_data]
        content = build_png_zip(
            project_id,
            title,
            include_ai_disclosure,
            collected.pages_data,
            collected.sources_preserved,
            [layout for layout, _ in checked],
        )
        key = f"exports/{project_id}/{uuid4().hex}/comic-pages.zip"
        url = self.storage_adapter.put(key, content, "application/zip")
        warnings = [warning for _, warning in checked if warning]
        return url, collected.sources_preserved, len(collected.pages_data), warnings
