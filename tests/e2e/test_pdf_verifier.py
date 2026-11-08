from __future__ import annotations

import zlib

from .pdf_verifier import verify_pdf_structure


def test_pdf_verifier_uses_declared_length_for_binary_streams() -> None:
    content = b"\nendstream\n".join(
        [
            b"MANDATORY AI-GENERATION DISCLOSURE",
            b"Meta generative models",
            b"AI-generated",
            b"Original Meta image files, with any embedded provenance metadata",
            b"does not verify C2PA signatures",
        ]
    )
    compressed = zlib.compress(content, level=0)
    metadata = b"icomic:provenanceRecords icomic:aiDisclosure <icomic:sourcesPreserved>True</icomic:sourcesPreserved>"
    pdf = (
        b"%PDF-1.4\n"
        + f"1 0 obj << /Length {len(compressed)} /Filter /FlateDecode >>\nstream\n".encode()
        + compressed
        + b"\nendstream\nendobj\n/Count 1\n"
        + metadata
        + b"\n%%EOF\n"
    )

    result = verify_pdf_structure(pdf, expected_page_count=1)

    assert result["stream_count"] == 1
