"""Overlay generation and replacement vector storage through both real services."""

import hashlib
import subprocess
from io import BytesIO

import httpx
import pytest
from PIL import Image


@pytest.mark.parametrize("go_api_server", [True], indirect=True)
def test_new_overlay_page_stores_layout_without_changing_source_art(api_client, ai_service, tmp_path):
    project = api_client.post(
        "/api/v1/projects",
        json={
            "title": "Lettering",
            "original_script": "Nia follows the brass bird to the lighthouse.",
            "art_style": "classic-comic",
            "chapter_count": 1,
        },
    ).json()["project"]
    project_id = project["id"]
    assert api_client.post(f"/api/v1/projects/{project_id}/analyze").status_code == 202
    for character in api_client.get(f"/api/v1/projects/{project_id}/characters").json()[
        "characters"
    ]:
        assert (
            api_client.post(f"/api/v1/characters/{character['id']}/reference-image").status_code
            == 202
        )
        assert api_client.post(f"/api/v1/characters/{character['id']}/approve").status_code == 200
    chapter = api_client.post(f"/api/v1/projects/{project_id}/chapters").json()["chapters"][0]
    assert api_client.post(f"/api/v1/chapters/{chapter['id']}/approve").status_code == 200
    response = api_client.post(f"/api/v1/chapters/{chapter['id']}/panels")
    assert response.status_code == 200, response.text
    page = response.json()["pages"][0]
    page_id = page["id"]
    assert page["lettering_mode"] == "overlay"
    endpoint = f"/api/v1/pages/{page_id}/lettering"
    # Fake language panels intentionally start with no dialogue. Edit through the
    # same public API the editor uses before asking for text-free art.
    assert (
        api_client.patch(
            f"/api/v1/panels/{page['panel_ids'][0]}",
            json={"dialogue": "Nia: What are you, little one?", "narration": "Before sunrise."},
        ).status_code
        == 200
    )
    response = api_client.get(endpoint).json()
    before = response["lettering"]
    assert before["version"] == "page-lettering-v1"
    assert before["layout"]["bubbles"]
    panel = before["input"]["panels"][0]
    utterance = next(u for u in panel["utterances"] if u["kind"] == "speech")
    assert panel["reserved_zones"]
    assert api_client.post(f"/api/v1/pages/{page_id}/draft").status_code == 202
    assert api_client.post(f"/api/v1/pages/{page_id}/draft/approve").status_code == 200
    painted = api_client.post(
        f"/api/v1/pages/{page_id}/painted-page", json={"variant": "direct-from-brief"}
    )
    assert painted.status_code == 200, painted.text
    image_url = painted.json()["image_url"]
    # Fake-storage URLs use port 8000; fetch from the owning ephemeral AI service.
    path = httpx.URL(image_url).path
    image = httpx.get(ai_service["base_url"] + path).content
    source_hash = hashlib.sha256(image).hexdigest()
    assert Image.open(BytesIO(image)).size == (256, 384)
    override_url = endpoint + f"/utterances/{utterance['utterance_id']}/override"
    edited = api_client.patch(override_url, json={"override": {"center": {"x": 0.5, "y": 0.2}}})
    assert edited.status_code == 200, edited.text
    state = edited.json()["lettering"]
    assert state["layout"] != before["layout"]
    assert state["overrides"][utterance["utterance_id"]]["center"] == {"x": 0.5, "y": 0.2}
    assert all(
        "placement" not in u or u["placement"] is None
        for p in state["input"]["panels"]
        for u in p["utterances"]
    )
    assert (
        api_client.patch(
            f"/api/v1/panels/{panel['panel_id']}",
            json={"dialogue": "Nia: Follow the light!", "narration": "Before dawn."},
        ).status_code
        == 200
    )
    state = api_client.get(endpoint).json()["lettering"]
    updated = next(
        u
        for u in state["input"]["panels"][0]["utterances"]
        if u["utterance_id"] == utterance["utterance_id"]
    )
    assert updated["text"] == "Follow the light!" and updated["speaker"] == "Nia"
    assert utterance["utterance_id"] in state["overrides"]
    # Editor-specific text/placement edits retain approval and source pixels.
    assert api_client.post(f"/api/v1/pages/{page_id}/draft/approve").status_code == 200
    assert api_client.post(f"/api/v1/pages/{page_id}/painted-page/approve").status_code == 200
    finished = api_client.post(f"/api/v1/pages/{page_id}/finish?variant=painted")
    assert finished.status_code == 200, finished.text
    finished_url = finished.json()["finished_image_url"]
    assert finished_url.startswith("http")
    raw_url = finished.json()["raw_master_image_url"]
    raw_bytes = httpx.get(ai_service["base_url"] + httpx.URL(raw_url).path).content
    assert hashlib.sha256(raw_bytes).hexdigest() == source_hash
    finished_url_again = api_client.post(f"/api/v1/pages/{page_id}/finish?variant=painted")
    assert finished_url_again.json()["finished_image_url"] == finished_url
    preset_change = api_client.patch(
        f"/api/v1/projects/{project_id}", json={"finish_preset": "print"}
    )
    assert preset_change.status_code == 200
    rederived = api_client.post(f"/api/v1/pages/{page_id}/finish?variant=painted")
    assert rederived.status_code == 200
    assert rederived.json()["finished_image_url"] != finished_url
    approved = next(
        p
        for p in api_client.get(f"/api/v1/chapters/{chapter['id']}/pages").json()["pages"]
        if p["id"] == page_id
    )
    text_url = endpoint + f"/utterances/{utterance['utterance_id']}/text"
    text_edit = api_client.patch(text_url, json={"text": "Home at last!"})
    assert text_edit.status_code == 200, text_edit.text
    state = text_edit.json()["lettering"]
    edited_utterance = next(
        u
        for u in state["input"]["panels"][0]["utterances"]
        if u["utterance_id"] == utterance["utterance_id"]
    )
    assert edited_utterance["text"] == "Home at last!" and edited_utterance["speaker"] == "Nia"
    assert state["overrides"][utterance["utterance_id"]]
    assert any(line["text"] for b in state["layout"]["bubbles"] for line in b["lines"])
    zones = api_client.patch(
        endpoint + "/zones",
        json={
            "panel_id": panel["panel_id"],
            "reserved_zones": [{"x": 0.05, "y": 0.03, "width": 0.9, "height": 0.5}],
        },
    )
    assert zones.status_code == 200, zones.text
    assert zones.json()["lettering"]["input"]["panels"][0]["reserved_zones"][0]["height"] == 0.5
    assert api_client.patch(override_url, json={"override": {}}).status_code == 200
    assert (
        utterance["utterance_id"] not in api_client.get(endpoint).json()["lettering"]["overrides"]
    )
    assert (
        hashlib.sha256(httpx.get(ai_service["base_url"] + path).content).hexdigest() == source_hash
    )
    pages = api_client.get(f"/api/v1/chapters/{chapter['id']}/pages").json()["pages"]
    saved = next(p for p in pages if p["id"] == page_id)
    assert saved["painted_page_image_url"] == image_url
    for field in ["draft_approved_at", "painted_page_approved_at", "updated_at"]:
        assert saved[field] == approved[field]
    source_panel = next(
        p
        for p in api_client.get(f"/api/v1/chapters/{chapter['id']}/panels").json()["panels"]
        if p["id"] == panel["panel_id"]
    )
    assert source_panel["dialogue"] == "Nia: Home at last!"
    assert saved["lettering"] == api_client.get(endpoint).json()["lettering"]
    exported = api_client.post(f"/api/v1/projects/{project_id}/export")
    assert exported.status_code == 202, exported.text
    pdf_url = exported.json()["export"]["file_url"]
    pdf = httpx.get(ai_service["base_url"] + httpx.URL(pdf_url).path).content
    output = tmp_path / "overlay.pdf"
    output.write_bytes(pdf)
    text = subprocess.check_output(["pdftotext", str(output), "-"], text=True)
    assert "Home at last!" in " ".join(text.split())
    assert not exported.json().get("warnings")
    assert len(pdf) < 500_000
    assert pdf.count(b"/Type /Page /Parent") == len(pages) + 1
    invalid = api_client.patch(override_url, json={"override": {"center": {"x": -0.5, "y": -0.5}}})
    assert invalid.status_code == 200, invalid.text
    flagged = api_client.post(f"/api/v1/projects/{project_id}/export")
    assert flagged.status_code == 202, flagged.text
    warnings = flagged.json()["warnings"]
    assert warnings[0]["code"] == "lettering_review_required"
    assert "out_of_panel" in warnings[0]["flags"]
    note_url = flagged.json()["export"]["file_url"]
    review = httpx.get(ai_service["base_url"] + httpx.URL(note_url).path).content
    note = subprocess.run(["pdftotext", "-", "-"], input=review, capture_output=True, check=True).stdout.decode()
    assert "LETTERING NOT EXPORTED" in note and "out_of_panel" in note
    # Explicit ink never consumes text-free art.
    assert (
        api_client.post(f"/api/v1/pages/{page_id}/line-art", json={"variant": "ink"}).status_code
        == 409
    )


@pytest.mark.parametrize(
    ("go_api_server", "overlay", "finish_preset"),
    [(True, True, "print-heavy"), (False, False, "print"), (False, False, "off")],
    indirect=["go_api_server"],
)
def test_standalone_fake_runner_overlay_flow_exports_pdf(
    monkeypatch, go_api_server, ai_service, tmp_path, overlay, finish_preset
):
    from scripts.e2e import comic_e2e as runner

    monkeypatch.setattr(runner, "API_URL", go_api_server["base_url"])
    monkeypatch.setattr(runner, "STORAGE_URL", ai_service["base_url"])
    monkeypatch.setattr(
        runner, "provision_run_user", lambda: "00000000-0000-0000-0000-000000000001"
    )
    guard = runner.CostGuard(float("inf"), False)
    guard.overlay_lettering = overlay
    guard.finish_preset = finish_preset
    count, project_id = runner.run_flow(guard, tmp_path)
    assert project_id and count > 0
    assert len(list(tmp_path.glob("lettering-*.json"))) == (count if overlay else 0)
    assert guard.pdf_page_count == count + 1
    assert (tmp_path / "latest.pdf").read_bytes().startswith(b"%PDF")
