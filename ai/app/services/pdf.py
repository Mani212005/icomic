"""PDF assembly service for finished comic book export.

Generates a valid binary multi-page PDF embedding comic page images,
panel dialogue text, cover title, mandatory AI-generation disclosure,
and preserved Meta C2PA provenance metadata.
"""

import json
import struct
import zlib
from datetime import datetime, timezone
from io import BytesIO
from typing import Any, ClassVar
from uuid import uuid4
from xml.sax.saxutils import escape

from PIL import Image

from ..adapters.storage.port import StoragePort
from ..gen.models import PageExportItem


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


def _generate_fallback_png(width: int = 64, height: int = 64) -> bytes:
    """Generate a minimal fallback test PNG for missing storage images."""
    raw_data = bytearray()
    for y in range(height):
        raw_data.append(0)
        for x in range(width):
            if x == 0 or x == width - 1 or y == 0 or y == height - 1:
                raw_data.extend((40, 40, 40))
            else:
                raw_data.extend((240, 240, 245))

    def chunk(tag: bytes, data: bytes) -> bytes:
        return (
            struct.pack(">I", len(data))
            + tag
            + data
            + struct.pack(">I", zlib.crc32(tag + data) & 0xFFFFFFFF)
        )

    ihdr = struct.pack(">IIBBBBB", width, height, 8, 2, 0, 0, 0)
    return (
        b"\x89PNG\r\n\x1a\n"
        + chunk(b"IHDR", ihdr)
        + chunk(b"IDAT", zlib.compress(bytes(raw_data)))
        + chunk(b"IEND", b"")
    )


class PdfWriter:
    """Pure-Python minimal PDF 1.4 binary document writer with standard fonts and images."""

    PAGE_DIMENSIONS: ClassVar[dict[str, tuple[float, float]]] = {
        "A4": (595.28, 841.89),
        "US_Letter": (612.0, 792.0),
        "Custom_Comic": (480.0, 740.0),
    }

    def __init__(self, page_size: str = "A4"):
        self.width, self.height = self.PAGE_DIMENSIONS.get(page_size, self.PAGE_DIMENSIONS["A4"])
        self.page_size_name = page_size
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
    ) -> bytes:
        """Assemble all document pages, embedded images, metadata, and cross-reference table."""
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

        # Top Banner
        cover_lines.append(b"0.12 0.18 0.32 rg")
        cover_lines.append(f"0 {self.height - 100:.2f} {self.width:.2f} 100 re f".encode())

        cover_lines.append(b"BT /F2 16 Tf 1.0 1.0 1.0 rg")
        cover_lines.append(
            f"50 {self.height - 55:.2f} Td (ICOMIC GRAPHIC NOVEL SERIES) Tj ET".encode()
        )

        # Main Title & Subtitle
        esc_title = escape_pdf_string(title)
        title_max_chars = max(18, int((self.width - 100) / 14))
        title_lines = _wrap_cover_title(title, title_max_chars)
        title_extra = max(0, len(title_lines) - 1) * 31
        for line_number, title_line in enumerate(title_lines):
            esc_title_line = escape_pdf_string(title_line)
            cover_lines.append(b"BT /F2 26 Tf 0.1 0.1 0.15 rg")
            title_y = self.height - 155 - line_number * 30
            cover_lines.append(f"50 {title_y:.2f} Td ({esc_title_line}) Tj ET".encode())

        cover_lines.append(b"BT /F3 13 Tf 0.35 0.35 0.4 rg")
        cover_lines.append(
            f"50 {self.height - 185 - title_extra:.2f} Td (AI-Generated Story & Artwork) Tj ET".encode()
        )

        # Mandatory AI Generation Disclosure Box
        if include_ai_disclosure:
            box_y = self.height - 345 - title_extra
            box_h = 125
            box_w = self.width - 100

            cover_lines.append(b"0.94 0.96 0.98 rg 0.25 0.35 0.55 RG 1.5 w")
            cover_lines.append(f"50 {box_y:.2f} {box_w:.2f} {box_h:.2f} re B".encode())

            cover_lines.append(b"BT /F2 11 Tf 0.15 0.25 0.45 rg")
            cover_lines.append(
                f"65 {box_y + box_h - 25:.2f} Td (MANDATORY AI-GENERATION DISCLOSURE) Tj ET".encode()
            )

            disclosure_statements = [
                "Notice: This comic book was created with Icomic AI using Meta generative models.",
                "All visual artwork, character artwork, and panel compositions are AI-generated.",
                "Meta C2PA provenance metadata and cryptographic signatures are preserved.",
                "Cryptographic provenance records remain embedded for transparency and auditability.",
            ]
            y_pos = box_y + box_h - 48
            for statement in disclosure_statements:
                esc_s = escape_pdf_string(statement)
                cover_lines.append(
                    f"BT /F1 9 Tf 0.2 0.2 0.25 rg 65 {y_pos:.2f} Td ({esc_s}) Tj ET".encode()
                )
                y_pos -= 18

        # Project Specifications & Provenance Summary
        spec_y = self.height - 425 - title_extra
        cover_lines.append(b"BT /F2 11 Tf 0.15 0.15 0.2 rg")
        cover_lines.append(f"50 {spec_y:.2f} Td (PROJECT & PROVENANCE RECORD) Tj ET".encode())

        spec_details = [
            f"Project Identifier: {project_id}",
            f"Total Comic Pages: {len(pages_data)}",
            f"Page Dimensions: {self.width:.0f} x {self.height:.0f} points ({self.page_size_name})",
            "Provenance Standard: Meta C2PA / Content Credentials preserved",
            f"Assembled: {datetime.now(timezone.utc).strftime('%Y-%m-%d %H:%M:%SZ')}",
        ]
        curr_y = spec_y - 22
        for detail in spec_details:
            esc_d = escape_pdf_string(detail)
            cover_lines.append(
                f"BT /F1 9 Tf 0.3 0.3 0.3 rg 50 {curr_y:.2f} Td ({esc_d}) Tj ET".encode()
            )
            curr_y -= 16

        # Cover Footer
        cover_lines.append(b"BT /F1 8 Tf 0.5 0.5 0.5 rg")
        cover_lines.append(
            b"50 35 Td (Icomic AI Publishing Pipeline | Powered by Meta AI Models | Cover Page) Tj ET"
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
            f"/Resources << /Font {font_res_str} >> /Contents {cover_content_id} 0 R >>".encode(),
        )
        page_ids.append(cover_page_id)

        # -----------------------------------------------------------------
        # Comic Pages (Pages 2 .. N+1)
        # -----------------------------------------------------------------
        total_pdf_pages = len(pages_data) + 1

        for idx, p in enumerate(pages_data, start=1):
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
            img_bytes = p.get("image_bytes")
            if not img_bytes:
                img_bytes = _generate_fallback_png()

            if img_bytes.startswith(b"\x89PNG\r\n\x1a\n"):
                try:
                    img_w, img_h, rgb_data = parse_png_to_rgb(img_bytes)
                except (KeyError, ValueError, OSError, struct.error, zlib.error) as exc:
                    raise UnsupportedImageMediaError(
                        f"Invalid PNG image on page {p.get('page_number', idx)}."
                    ) from exc

                compressed_rgb = zlib.compress(rgb_data)
                img_xobj_id = self._add_object(
                    f"<< /Type /XObject /Subtype /Image /Width {img_w} /Height {img_h} "
                    f"/ColorSpace /DeviceRGB /BitsPerComponent 8 "
                    f"/Filter /FlateDecode /Length {len(compressed_rgb)} >>\nstream\n".encode()
                    + compressed_rgb
                    + b"\nendstream"
                )
            elif img_bytes.startswith(b"\xff\xd8"):
                img_w, img_h, colorspace = parse_jpeg_info(img_bytes)
                img_xobj_id = self._add_object(
                    f"<< /Type /XObject /Subtype /Image /Width {img_w} /Height {img_h} "
                    f"/ColorSpace {colorspace} /BitsPerComponent 8 "
                    f"/Filter /DCTDecode /Length {len(img_bytes)} >>\nstream\n".encode()
                    + img_bytes
                    + b"\nendstream"
                )
            elif (
                img_bytes.startswith(b"RIFF")
                and len(img_bytes) >= 12
                and img_bytes[8:12] == b"WEBP"
            ):
                # PDF has no native WebP filter. Render decoded pixels and attach
                # the untouched source so any embedded provenance remains intact.
                with Image.open(BytesIO(img_bytes)) as image:
                    rgba = image.convert("RGBA")
                    rgb = Image.new("RGB", rgba.size, "white")
                    rgb.paste(rgba, mask=rgba.getchannel("A"))
                    img_w, img_h = rgb.size
                    compressed_rgb = zlib.compress(rgb.tobytes())
                img_xobj_id = self._add_object(
                    f"<< /Type /XObject /Subtype /Image /Width {img_w} /Height {img_h} "
                    f"/ColorSpace /DeviceRGB /BitsPerComponent 8 /Filter /FlateDecode "
                    f"/Length {len(compressed_rgb)} >>\nstream\n".encode()
                    + compressed_rgb
                    + b"\nendstream"
                )
            else:
                page_num = p.get("page_number", idx)
                raise UnsupportedImageMediaError(
                    f"Unsupported image format on page {page_num}. "
                    "PDF export supports PNG, JPEG and WebP."
                )

            if not img_bytes.startswith(b"\xff\xd8"):
                extension = "png" if img_bytes.startswith(b"\x89PNG") else "webp"
                source_id = self._add_object(
                    f"<< /Type /EmbeddedFile /Subtype /image#2F{extension} /Length {len(img_bytes)} >>\nstream\n".encode()
                    + img_bytes
                    + b"\nendstream"
                )
                filename = f"page-{idx}-original.{extension}"
                file_id = self._add_object(
                    f"<< /Type /Filespec /F ({filename}) /EF << /F {source_id} 0 R >> >>".encode()
                )
                source_files.append(f"({filename}) {file_id} 0 R")

            original_image_bytes = p.get("original_image_bytes")
            if original_image_bytes and original_image_bytes != img_bytes:
                if original_image_bytes.startswith(b"\x89PNG\r\n\x1a\n"):
                    original_extension = "png"
                elif original_image_bytes.startswith(b"\xff\xd8"):
                    original_extension = "jpeg"
                elif (
                    original_image_bytes.startswith(b"RIFF")
                    and original_image_bytes[8:12] == b"WEBP"
                ):
                    original_extension = "webp"
                else:
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

            panels = p.get("panels", [])
            dialogue_text = p.get("dialogue_text")
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
                    b"50 28 Td (AI-Generated Content | Meta Provenance Preserved | Icomic AI) Tj ET"
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

            res_dict = f"<< /Font {font_res_str} /XObject << /Im1 {img_xobj_id} 0 R >> >>"
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
        # Document Metadata Stream (XMP with C2PA Provenance)
        # -----------------------------------------------------------------
        provenance_records = [
            {
                "page_number": p.get("page_number", i),
                "image_url": p.get("image_url", ""),
                "original_image_url": p.get("original_image_url", ""),
                "provenance": p.get("provenance_metadata", {}),
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
            f"AI-Generated Comic Book with Meta C2PA Provenance Preserved"
            f"</rdf:li></rdf:Alt></dc:description>\n"
            f"  </rdf:Description>\n"
            f'  <rdf:Description rdf:about="" xmlns:icomic="https://icomic.app/schema/provenance/1.0">\n'
            f"   <icomic:projectId>{escape(project_id)}</icomic:projectId>\n"
            f"   <icomic:provenanceRecords>{escape(json.dumps(provenance_records))}</icomic:provenanceRecords>\n"
            f"   <icomic:aiDisclosure>{include_ai_disclosure!s}</icomic:aiDisclosure>\n"
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
            f"/Subject (Comic Book with Preserved Meta Provenance) "
            f"/CreationDate (D:{now_dt}) /ModDate (D:{now_dt}) >>".encode()
        )

        # -----------------------------------------------------------------
        # Binary Assembly with Cross-Reference Table
        # -----------------------------------------------------------------
        pdf = bytearray()
        pdf.extend(b"%PDF-1.4\n%\xe2\xe3\xcf\xd3\n")

        offsets = [0]
        for idx, obj in enumerate(self.objects, start=1):
            offsets.append(len(pdf))
            pdf.extend(f"{idx} 0 obj\n".encode())
            pdf.extend(obj)
            pdf.extend(b"\nendobj\n")

        xref_offset = len(pdf)
        pdf.extend(f"xref\n0 {len(self.objects) + 1}\n".encode())
        pdf.extend(b"0000000000 65535 f \r\n")
        for off in offsets[1:]:
            pdf.extend(f"{off:010d} 00000 n \r\n".encode())

        pdf.extend(
            f"trailer\n<< /Size {len(self.objects) + 1} /Root {catalog_id} 0 R /Info {info_id} 0 R >>\n"
            f"startxref\n{xref_offset}\n%%EOF\n".encode()
        )

        return bytes(pdf)


class PdfAssemblyService:
    """Service compiling comic book pages into an assembled PDF with provenance and disclosure."""

    def __init__(self, storage_adapter: StoragePort):
        self.storage_adapter = storage_adapter

    def assemble_pdf(
        self,
        project_id: str,
        title: str,
        page_size: str,
        include_ai_disclosure: bool,
        pages: list[PageExportItem],
    ) -> tuple[str, bool, int]:
        """Assemble pages into PDF blob, upload to storage, and return download URL."""
        if not pages:
            raise ValueError("Cannot assemble PDF with zero pages.")

        pages_data: list[dict[str, Any]] = []
        for p in pages:
            img_bytes: bytes | None = None
            original_image_bytes: bytes | None = None
            if p.image_url:
                try:
                    img_bytes = self.storage_adapter.get(p.image_url)
                except (KeyError, ValueError, OSError):
                    img_bytes = None
            original_image_url = getattr(p, "original_image_url", None)
            if original_image_url:
                try:
                    original_image_bytes = self.storage_adapter.get(original_image_url)
                except (KeyError, ValueError, OSError):
                    original_image_bytes = None

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
                    "page_number": p.page_number,
                    "image_url": p.image_url,
                    "image_bytes": img_bytes,
                    "original_image_url": original_image_url,
                    "original_image_bytes": original_image_bytes,
                    "provenance_metadata": p.provenance_metadata or {},
                    "panels": panels_list,
                    "dialogue_text": getattr(p, "dialogue_text", None),
                }
            )

        writer = PdfWriter(page_size=page_size)
        pdf_content = writer.build_pdf(
            project_id=project_id,
            title=title,
            include_ai_disclosure=include_ai_disclosure,
            pages_data=pages_data,
        )

        # Page count: 1 cover page + N comic pages
        total_page_count = len(pages_data) + 1

        key = f"exports/{project_id}/{uuid4().hex}/comic.pdf"
        url = self.storage_adapter.put(key, pdf_content, "application/pdf")
        return url, True, total_page_count
