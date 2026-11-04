"""End-to-end happy path test driving a complete comic project through HTTP.

Exercises the entire user journey against live local Go API and Python AI processes:
sign-in -> create project -> submit script -> analyse -> approve characters ->
generate & approve chapter outline -> panels & pages -> draft sketches ->
approve drafts -> line art -> approve line art -> colour palette suggestion ->
export -> download PDF -> structural PDF verification (disclosure & provenance).
"""

from __future__ import annotations

from io import BytesIO

import httpx
from PIL import Image

try:
    from .pdf_verifier import verify_pdf_structure
except ImportError:
    from pdf_verifier import verify_pdf_structure


def test_complete_comic_lifecycle_e2e(api_client: httpx.Client) -> None:
    """Drive a complete comic creation project through the real Go API and Python AI service."""

    # -------------------------------------------------------------------------
    # 1. Sign-in via supported dev identity
    # -------------------------------------------------------------------------
    me_res = api_client.get("/api/v1/me/credit-usage")
    assert me_res.status_code == 200, f"Sign-in / credit usage failed: {me_res.text}"
    user_info = me_res.json()
    assert "balance" in user_info, "Expected balance in user credit usage"
    assert user_info["balance"] >= 10.0, (
        f"Expected adequate initial credit, got {user_info['balance']}"
    )

    # -------------------------------------------------------------------------
    # 2. Create Project
    # -------------------------------------------------------------------------
    project_payload = {
        "title": "The Quantum Detective",
        "original_script": (
            "Chapter 1: The neon rain fell over Sector 7. Jax ignited his cyber-pipe and scanned the alleys. "
            "Chapter 2: The syndicate mainframe erupted in sparks as Maya breached the airlock."
        ),
        "art_style": "cyberpunk-noir",
        "chapter_count": 2,
    }
    proj_res = api_client.post("/api/v1/projects", json=project_payload)
    assert proj_res.status_code == 201, f"Project creation failed: {proj_res.text}"
    proj_data = proj_res.json()
    assert "project" in proj_data, "Expected project in response"
    project_id = proj_data["project"]["id"]
    assert project_id, "Project ID must not be empty"

    # -------------------------------------------------------------------------
    # 3. Submit Script / Analyse
    # -------------------------------------------------------------------------
    analyze_res = api_client.post(f"/api/v1/projects/{project_id}/analyze")
    assert analyze_res.status_code == 202, f"Script analysis failed: {analyze_res.text}"
    analyze_data = analyze_res.json()
    assert "character_profiles" in analyze_data, "Expected character profiles in analysis response"

    # -------------------------------------------------------------------------
    # 4. Approve Characters
    # -------------------------------------------------------------------------
    chars_res = api_client.get(f"/api/v1/projects/{project_id}/characters")
    assert chars_res.status_code == 200, f"List characters failed: {chars_res.text}"
    characters = chars_res.json().get("characters", [])
    assert len(characters) > 0, "Expected at least one extracted character"

    for char in characters:
        char_id = char["id"]
        # Generate reference turnaround image
        ref_res = api_client.post(f"/api/v1/characters/{char_id}/reference-image")
        assert ref_res.status_code == 202, f"Generate reference image failed: {ref_res.text}"

        # Approve character reference
        app_char_res = api_client.post(f"/api/v1/characters/{char_id}/approve")
        assert app_char_res.status_code == 200, f"Approve character failed: {app_char_res.text}"

    # -------------------------------------------------------------------------
    # 5. Generate and Approve Chapter Outline
    # -------------------------------------------------------------------------
    chapters_res = api_client.post(f"/api/v1/projects/{project_id}/chapters")
    assert chapters_res.status_code == 200, f"Generate chapters failed: {chapters_res.text}"
    chapters = chapters_res.json().get("chapters", [])
    assert len(chapters) > 0, "Expected at least one chapter generated"

    for chapter in chapters:
        chap_id = chapter["id"]
        app_chap_res = api_client.post(f"/api/v1/chapters/{chap_id}/approve")
        assert app_chap_res.status_code == 200, f"Approve chapter failed: {app_chap_res.text}"

    target_chapter_id = chapters[0]["id"]

    # -------------------------------------------------------------------------
    # 6. Generate Panels and Pages
    # -------------------------------------------------------------------------
    panels_res = api_client.post(f"/api/v1/chapters/{target_chapter_id}/panels")
    assert panels_res.status_code == 200, f"Generate panels failed: {panels_res.text}"
    panels_data = panels_res.json()
    panels = panels_data.get("panels", [])
    pages = panels_data.get("pages", [])
    assert len(panels) > 0, "Expected at least one panel generated"
    assert len(pages) > 0, "Expected at least one page partitioned"

    # Verify pages can be fetched via GET endpoint
    get_pages_res = api_client.get(f"/api/v1/chapters/{target_chapter_id}/pages")
    assert get_pages_res.status_code == 200, f"List pages failed: {get_pages_res.text}"

    target_page_id = pages[0]["id"]

    # -------------------------------------------------------------------------
    # 7. Draft Sketches
    # -------------------------------------------------------------------------
    draft_res = api_client.post(f"/api/v1/pages/{target_page_id}/draft")
    assert draft_res.status_code == 202, f"Generate draft sketch failed: {draft_res.text}"

    # -------------------------------------------------------------------------
    # 8. Approve Drafts
    # -------------------------------------------------------------------------
    app_draft_res = api_client.post(f"/api/v1/pages/{target_page_id}/draft/approve")
    assert app_draft_res.status_code == 200, f"Approve draft sketch failed: {app_draft_res.text}"

    # -------------------------------------------------------------------------
    # 9. Line Art
    # -------------------------------------------------------------------------
    line_res = api_client.post(f"/api/v1/pages/{target_page_id}/line-art")
    assert line_res.status_code == 202, f"Generate line art failed: {line_res.text}"

    # -------------------------------------------------------------------------
    # 10. Approve Line Art
    # -------------------------------------------------------------------------
    app_line_res = api_client.post(f"/api/v1/pages/{target_page_id}/line-art/approve")
    assert app_line_res.status_code == 200, f"Approve line art failed: {app_line_res.text}"

    # -------------------------------------------------------------------------
    # 11. Colour Palette Suggestion
    # -------------------------------------------------------------------------
    col_res = api_client.post(f"/api/v1/pages/{target_page_id}/colour-palette/suggest")
    assert col_res.status_code == 202, f"Suggest colour palette failed: {col_res.text}"

    png_buffer = BytesIO()
    Image.new("RGB", (3, 3), "red").save(png_buffer, format="PNG")
    colour_image_res = api_client.put(
        f"/api/v1/pages/{target_page_id}/colour-image",
        content=png_buffer.getvalue(),
        headers={"Content-Type": "image/png"},
    )
    assert colour_image_res.status_code == 200, (
        f"Upload coloured page failed: {colour_image_res.text}"
    )
    assert colour_image_res.json().get("colour_image_url"), "Expected a stored coloured page URL"

    app_col_res = api_client.post(f"/api/v1/pages/{target_page_id}/colour/approve")
    assert app_col_res.status_code == 200, f"Approve colour failed: {app_col_res.text}"

    # -------------------------------------------------------------------------
    # 12. Export Project
    # -------------------------------------------------------------------------
    export_res = api_client.post(f"/api/v1/projects/{project_id}/export")
    assert export_res.status_code == 202, f"Export project failed: {export_res.text}"
    export_record = export_res.json().get("export", {})
    export_id = export_record.get("id")
    assert export_id, "Expected export id in response"

    get_export_res = api_client.get(f"/api/v1/exports/{export_id}")
    assert get_export_res.status_code == 200, f"Get export failed: {get_export_res.text}"
    assert get_export_res.json()["export"]["status"] == "completed"

    # -------------------------------------------------------------------------
    # 13. Download PDF through Go Download Endpoint
    # -------------------------------------------------------------------------
    download_res = api_client.get(f"/api/v1/exports/{export_id}/download")
    assert download_res.status_code == 200, f"Download export failed: {download_res.text}"
    content_type = download_res.headers.get("Content-Type", "")
    assert "application/pdf" in content_type, (
        f"Expected application/pdf content type, got '{content_type}'"
    )

    pdf_bytes = download_res.content
    assert len(pdf_bytes) > 0, "Downloaded PDF bytes must not be empty"

    # -------------------------------------------------------------------------
    # 14. Structural PDF Verification (page count, disclosure, provenance)
    # -------------------------------------------------------------------------
    # Total pages = 1 cover page + N comic pages (len(pages) for target chapter, or at least 2 pages)
    expected_pages = len(pages) + 1
    verification = verify_pdf_structure(
        pdf_bytes,
        expected_page_count=expected_pages,
        project_id=project_id,
    )
    assert verification["valid_header"] is True
    assert verification["valid_trailer"] is True
    assert verification["ai_disclosure_verified"] is True
    assert verification["provenance_metadata_verified"] is True
    assert verification["project_id_verified"] is True
