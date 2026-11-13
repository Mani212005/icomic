"""End-to-end export correctness: readiness refusal, page size, PNG ZIP and provenance."""

from __future__ import annotations

import io
import json
import zipfile

import httpx


def painted_project(api_client: httpx.Client, approve: bool = True) -> tuple[str, int]:
    """Drive a one-chapter project to a painted page, optionally approved; return its id and page count."""
    created = api_client.post(
        "/api/v1/projects",
        json={
            "title": "The Brass Bird",
            "original_script": "Nia stands at the storm-swept lighthouse holding the glowing brass bird.",
            "art_style": "classic-comic",
            "chapter_count": 1,
        },
    )
    assert created.status_code == 201, created.text
    project_id = created.json()["project"]["id"]
    api_client.post(f"/api/v1/projects/{project_id}/analyze")
    for character in api_client.get(f"/api/v1/projects/{project_id}/characters").json()["characters"]:
        api_client.post(f"/api/v1/characters/{character['id']}/reference-image")
        assert api_client.post(f"/api/v1/characters/{character['id']}/approve").status_code == 200
    chapter_id = api_client.post(f"/api/v1/projects/{project_id}/chapters").json()["chapters"][0]["id"]
    assert api_client.post(f"/api/v1/chapters/{chapter_id}/approve").status_code == 200
    pages = api_client.post(f"/api/v1/chapters/{chapter_id}/panels").json()["pages"]
    page_id = pages[0]["id"]
    assert api_client.post(f"/api/v1/pages/{page_id}/draft").status_code == 202
    assert api_client.post(f"/api/v1/pages/{page_id}/draft/approve").status_code == 200
    assert api_client.post(f"/api/v1/pages/{page_id}/line-art").status_code == 202
    if approve:
        assert api_client.post(f"/api/v1/pages/{page_id}/line-art/approve").status_code == 200
    return project_id, len(pages)


def test_export_is_refused_until_the_page_art_is_approved(api_client: httpx.Client) -> None:
    project_id, _ = painted_project(api_client, approve=False)
    refused = api_client.post(f"/api/v1/projects/{project_id}/export")
    assert refused.status_code == 409, refused.text
    body = refused.json()
    assert body["code"] == "EXPORT_NOT_READY"
    assert body["details"]["problems"][0]["code"] == "missing_approved_art"


def test_selected_page_size_reaches_the_downloaded_pdf(api_client: httpx.Client) -> None:
    project_id, page_count = painted_project(api_client)
    for page_size, media_box in [
        (None, b"/MediaBox [0 0 595.28 841.89]"),
        ("a4", b"/MediaBox [0 0 595.28 841.89]"),
        ("us_letter", b"/MediaBox [0 0 612.00 792.00]"),
        ("custom", b"/MediaBox [0 0 477.00 738.00]"),
    ]:
        body = {"format": "pdf"} if page_size is None else {"format": "pdf", "page_size": page_size}
        exported = api_client.post(f"/api/v1/projects/{project_id}/export", json=body)
        assert exported.status_code == 202, exported.text
        export = exported.json()["export"]
        assert export["format"] == "pdf" and export["provenance_metadata_preserved"] is True
        download = api_client.get(f"/api/v1/exports/{export['id']}/download", timeout=30.0)
        assert download.status_code == 200
        assert download.headers["content-type"] == "application/pdf"
        assert download.content.count(media_box) == page_count + 1, page_size
    rejected = api_client.post(f"/api/v1/projects/{project_id}/export", json={"page_size": "tabloid"})
    assert rejected.status_code == 400


def test_png_export_downloads_a_zip_of_pages_with_their_provider_sources(
    api_client: httpx.Client,
) -> None:
    project_id, page_count = painted_project(api_client)
    exported = api_client.post(f"/api/v1/projects/{project_id}/export", json={"format": "png"})
    assert exported.status_code == 202, exported.text
    export = exported.json()["export"]
    assert export["format"] == "png_zip" and export["provenance_metadata_preserved"] is True

    download = api_client.get(f"/api/v1/exports/{export['id']}/download", timeout=30.0)
    assert download.status_code == 200
    assert download.headers["content-type"] == "application/zip"
    assert download.headers["content-disposition"].endswith('.zip"')
    assert download.content[:4] == b"PK\x03\x04"
    with zipfile.ZipFile(io.BytesIO(download.content)) as archive:
        pngs = [name for name in archive.namelist() if name.startswith("pages/")]
        assert pngs == [f"pages/page-{n:03d}.png" for n in range(1, page_count + 1)]
        assert all(archive.read(name)[:8] == b"\x89PNG\r\n\x1a\n" for name in pngs)
        manifest = json.loads(archive.read("manifest.json"))
        assert manifest["provenance_metadata_preserved"] is True
        assert [page["page_number"] for page in manifest["pages"]] == list(range(1, page_count + 1))
        assert all(page["source_verified"] for page in manifest["pages"])
        assert "AI-generation disclosure" in archive.read("DISCLOSURE.txt").decode()
