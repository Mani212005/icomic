"""End-to-end integration tests for the painted default and explicit ink finish."""

from __future__ import annotations

import httpx
from conftest import artifact_url

try:
    from .pdf_verifier import verify_pdf_structure
except ImportError:
    from pdf_verifier import verify_pdf_structure


def test_painted_page_route_e2e(api_client: httpx.Client) -> None:
    """Drive opt-in painted-page generation, approval, and PDF export through the real processes."""
    # 1. Create project
    proj_res = api_client.post(
        "/api/v1/projects",
        json={
            "title": "The Brass Bird",
            "original_script": "Nia stands at the storm-swept lighthouse holding the glowing brass bird.",
            "art_style": "classic-comic",
            "chapter_count": 1,
        },
    )
    assert proj_res.status_code == 201, proj_res.text
    project_id = proj_res.json()["project"]["id"]

    # 2. Analyze script & approve characters
    api_client.post(f"/api/v1/projects/{project_id}/analyze")
    chars_res = api_client.get(f"/api/v1/projects/{project_id}/characters")
    characters = chars_res.json()["characters"]
    for char in characters:
        api_client.post(f"/api/v1/characters/{char['id']}/reference-image")
        app_res = api_client.post(f"/api/v1/characters/{char['id']}/approve")
        assert app_res.status_code == 200, app_res.text

    # 3. Chapters & outline
    chap_res = api_client.post(f"/api/v1/projects/{project_id}/chapters")
    chapters = chap_res.json()["chapters"]
    chapter_id = chapters[0]["id"]
    app_chap_res = api_client.post(f"/api/v1/chapters/{chapter_id}/approve")
    assert app_chap_res.status_code == 200, app_chap_res.text

    # 4. Panels & pages
    pages_res = api_client.post(f"/api/v1/chapters/{chapter_id}/panels")
    pages = pages_res.json()["pages"]
    page_id = pages[0]["id"]

    # 5. Generate and approve draft composition
    draft_res = api_client.post(f"/api/v1/pages/{page_id}/draft")
    assert draft_res.status_code == 202, draft_res.text
    app_draft_res = api_client.post(f"/api/v1/pages/{page_id}/draft/approve")
    assert app_draft_res.status_code == 200, app_draft_res.text

    # 6. Default finish route creates one direct-from-brief painted page.
    painted_res = api_client.post(f"/api/v1/pages/{page_id}/line-art")
    assert painted_res.status_code == 202, painted_res.text
    painted_data = painted_res.json()
    assert painted_data["variant"] == "direct-from-brief"
    assert artifact_url(api_client, painted_data).startswith("http")
    assert api_client.get(f"/api/v1/generation-jobs/{painted_data['job_id']}").json()["job"]["status"] == "done"

    # 7. Approve finished painted page
    app_painted_res = api_client.post(f"/api/v1/pages/{page_id}/line-art/approve")
    assert app_painted_res.status_code == 200, app_painted_res.text
    assert app_painted_res.json()["status"] == "approved"

    # 8. Export project choosing approved painted finish
    export_res = api_client.post(f"/api/v1/projects/{project_id}/export")
    assert export_res.status_code == 202, export_res.text
    export_id = export_res.json()["export"]["id"]

    get_exp_res = api_client.get(f"/api/v1/exports/{export_id}")
    assert get_exp_res.status_code == 200, get_exp_res.text
    assert get_exp_res.json()["export"]["status"] == "completed"

    download_res = api_client.get(f"/api/v1/exports/{export_id}/download", timeout=30.0)
    assert download_res.status_code == 200, download_res.text
    assert len(download_res.content) > 0

    verification = verify_pdf_structure(
        download_res.content,
        expected_page_count=len(pages) + 1,
        project_id=project_id,
    )
    assert verification["valid_header"] is True
    assert verification["valid_trailer"] is True
    assert verification["ai_disclosure_verified"] is True
    assert verification["provenance_metadata_verified"] is True


def test_painted_page_direct_from_brief_e2e(api_client: httpx.Client) -> None:
    """Verify that direct-from-brief variant generates without requiring draft generation."""
    # Create project
    proj_res = api_client.post(
        "/api/v1/projects",
        json={
            "title": "Direct From Brief",
            "original_script": "Mira explores the clockwork cave.",
            "art_style": "classic-comic",
            "chapter_count": 1,
        },
    )
    assert proj_res.status_code == 201, proj_res.text
    project_id = proj_res.json()["project"]["id"]

    # Characters
    api_client.post(f"/api/v1/projects/{project_id}/analyze")
    chars_res = api_client.get(f"/api/v1/projects/{project_id}/characters")
    for char in chars_res.json()["characters"]:
        api_client.post(f"/api/v1/characters/{char['id']}/reference-image")
        api_client.post(f"/api/v1/characters/{char['id']}/approve")

    # Outline
    chap_res = api_client.post(f"/api/v1/projects/{project_id}/chapters")
    chapter_id = chap_res.json()["chapters"][0]["id"]
    api_client.post(f"/api/v1/chapters/{chapter_id}/approve")

    # Panels & page
    pages_res = api_client.post(f"/api/v1/chapters/{chapter_id}/panels")
    pages = pages_res.json().get("pages", [])
    assert len(pages) > 0

    # Generate painted page directly from brief for each page (bypassing draft sketch!)
    for page in pages:
        p_id = page["id"]
        direct_res = api_client.post(
            f"/api/v1/pages/{p_id}/painted-page",
            json={"variant": "direct-from-brief"},
        )
        assert direct_res.status_code == 202, direct_res.text
        direct_data = direct_res.json()
        assert direct_data["variant"] == "direct-from-brief"
        assert artifact_url(api_client, direct_data).startswith("http")

        app_res = api_client.post(f"/api/v1/pages/{p_id}/painted-page/approve")
        assert app_res.status_code == 200, app_res.text

    export_res = api_client.post(f"/api/v1/projects/{project_id}/export")
    assert export_res.status_code == 202, export_res.text
    export_id = export_res.json()["export"]["id"]

    get_exp_res = api_client.get(f"/api/v1/exports/{export_id}")
    assert get_exp_res.status_code == 200, get_exp_res.text
    assert get_exp_res.json()["export"]["status"] == "completed"

    download_res = api_client.get(f"/api/v1/exports/{export_id}/download", timeout=30.0)
    assert download_res.status_code == 200, download_res.text
    assert len(download_res.content) > 0


def test_explicit_ink_finish_e2e(api_client: httpx.Client) -> None:
    """Verify ink stays available by explicit variant and exports without a painted page."""
    project = api_client.post(
        "/api/v1/projects",
        json={"title": "Ink finish", "original_script": "Nia watches the harbor.", "art_style": "classic-comic", "chapter_count": 1},
    ).json()["project"]
    project_id = project["id"]
    api_client.post(f"/api/v1/projects/{project_id}/analyze")
    for character in api_client.get(f"/api/v1/projects/{project_id}/characters").json()["characters"]:
        api_client.post(f"/api/v1/characters/{character['id']}/reference-image")
        api_client.post(f"/api/v1/characters/{character['id']}/approve")
    chapter_id = api_client.post(f"/api/v1/projects/{project_id}/chapters").json()["chapters"][0]["id"]
    api_client.post(f"/api/v1/chapters/{chapter_id}/approve")
    page_id = api_client.post(f"/api/v1/chapters/{chapter_id}/panels").json()["pages"][0]["id"]
    api_client.post(f"/api/v1/pages/{page_id}/draft")
    api_client.post(f"/api/v1/pages/{page_id}/draft/approve")

    ink = api_client.post(f"/api/v1/pages/{page_id}/line-art?variant=ink")
    assert ink.status_code == 202, ink.text
    assert artifact_url(api_client, ink.json()).startswith("http")
    approved = api_client.post(f"/api/v1/pages/{page_id}/line-art/approve?variant=ink")
    assert approved.status_code == 200, approved.text
    assert approved.json()["page"]["line_art_approved_at"]
    assert not approved.json()["page"].get("painted_page_image_url")
