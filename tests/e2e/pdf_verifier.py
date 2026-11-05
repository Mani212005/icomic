"""Structural verifier for comic book export PDFs.

Validates binary PDF headers and trailers, page counts, flate-compressed stream
contents (mandatory AI-generation disclosures), and embedded XMP C2PA provenance metadata.
"""

from __future__ import annotations

import re
import zlib
from typing import Any


def verify_pdf_structure(
    pdf_bytes: bytes,
    expected_page_count: int | None = None,
    project_id: str | None = None,
) -> dict[str, Any]:
    """Verify the structural integrity, page count, disclosures, and provenance of a comic export PDF.

    Raises AssertionError if any structural or mandatory requirement fails.
    Returns a dictionary of verified properties.
    """
    assert len(pdf_bytes) > 0, "PDF byte stream is empty"

    # 1. Binary PDF header signature (PDF 1.4 or higher)
    assert pdf_bytes.startswith(b"%PDF-"), (
        f"PDF does not start with %PDF- signature, got: {pdf_bytes[:10]!r}"
    )

    # 2. End-of-file trailer signature
    assert b"%%EOF" in pdf_bytes, "PDF missing %%EOF trailer marker"

    # 3. Page Count verification
    # Look for /Type /Pages /Kids [...] /Count N or /Count N
    count_matches = re.findall(rb"/Count\s+(\d+)", pdf_bytes)
    assert len(count_matches) > 0, "Could not find /Count in PDF pages tree"
    page_count = int(count_matches[0])

    if expected_page_count is not None:
        assert page_count == expected_page_count, (
            f"Expected {expected_page_count} PDF pages, got {page_count}"
        )

    # 4. Decompress streams to inspect rendered text. Stream bytes are binary, so
    # locating `endstream` with a regex can stop early if those bytes happen to
    # contain that sequence. The writer emits direct numeric /Length values.
    decompressed_streams: list[bytes] = []
    position = 0
    stream_header = re.compile(rb"/Length\s+(\d+)[^>]*>>\s*stream\r?\n")
    while match := stream_header.search(pdf_bytes, position):
        stream_length = int(match.group(1))
        stream_start = match.end()
        stream_end = stream_start + stream_length
        assert stream_end <= len(pdf_bytes), "PDF stream exceeds the file length"
        stream = pdf_bytes[stream_start:stream_end]
        try:
            decompressed_streams.append(zlib.decompress(stream))
        except Exception:
            # Stream might not be zlib-compressed (e.g. raw metadata or already plain text)
            decompressed_streams.append(stream)
        position = stream_end

    all_content_text = b" ".join(decompressed_streams) + b" " + pdf_bytes

    # 5. Mandatory AI-Generation Disclosure
    mandatory_terms = [
        b"MANDATORY AI-GENERATION DISCLOSURE",
        b"Meta generative models",
        b"AI-generated",
        b"Meta C2PA provenance metadata",
    ]
    for term in mandatory_terms:
        assert term in all_content_text, (
            f"Mandatory AI disclosure term missing from PDF text: {term.decode('utf-8', errors='replace')}"
        )

    # 6. Provenance Metadata (XMP packet & C2PA records)
    provenance_terms = [
        b"icomic:provenanceRecords",
        b"icomic:aiDisclosure",
        b"c2pa",
    ]
    for term in provenance_terms:
        assert term in pdf_bytes, (
            f"Provenance metadata term missing from PDF XMP metadata: {term.decode('utf-8', errors='replace')}"
        )

    if project_id:
        proj_term = f"<icomic:projectId>{project_id}</icomic:projectId>".encode("utf-8")
        assert proj_term in pdf_bytes, (
            f"Project ID {project_id} not embedded in PDF XMP metadata"
        )

    return {
        "valid_header": True,
        "valid_trailer": True,
        "page_count": page_count,
        "stream_count": len(decompressed_streams),
        "ai_disclosure_verified": True,
        "provenance_metadata_verified": True,
        "project_id_verified": bool(project_id),
    }
