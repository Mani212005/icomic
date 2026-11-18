"""Overlay generation and replacement vector storage through both real services."""

import hashlib
import json
import subprocess
import zipfile
from io import BytesIO

import httpx
import pytest
from PIL import Image
from conftest import artifact_url


@pytest.mark.parametrize("go_api_server", [True], indirect=True)
def test_new_overlay_page_stores_layout_without_changing_source_art(api_client, tmp_path):
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
    assert painted.status_code == 202, painted.text
    image_url = artifact_url(api_client, painted.json())
    # The API hands out a short-lived scoped link; the browser fetches the image through it.
    image = httpx.get(image_url).content
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
    raw_bytes = httpx.get(raw_url).content
    assert hashlib.sha256(raw_bytes).hexdigest() == source_hash
    finished_url_again = api_client.post(f"/api/v1/pages/{page_id}/finish?variant=painted")
    assert httpx.URL(finished_url_again.json()["finished_image_url"]).path == httpx.URL(finished_url).path
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
            "reserved_zones": [{"x": 0.05, "y": 0.03, "width": 0.9, "height": 0.8}],
        },
    )
    assert zones.status_code == 200, zones.text
    assert zones.json()["lettering"]["input"]["panels"][0]["reserved_zones"][0]["height"] == 0.8
    assert api_client.patch(override_url, json={"override": {}}).status_code == 200
    assert (
        utterance["utterance_id"] not in api_client.get(endpoint).json()["lettering"]["overrides"]
    )
    assert hashlib.sha256(httpx.get(image_url).content).hexdigest() == source_hash
    pages = api_client.get(f"/api/v1/chapters/{chapter['id']}/pages").json()["pages"]
    saved = next(p for p in pages if p["id"] == page_id)
    assert httpx.URL(saved["painted_page_image_url"]).path == httpx.URL(image_url).path
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
    pdf = httpx.get(pdf_url).content
    output = tmp_path / "overlay.pdf"
    output.write_bytes(pdf)
    text = subprocess.check_output(["pdftotext", str(output), "-"], text=True)
    assert "Home at last!" in " ".join(text.split())
    assert not exported.json().get("warnings")
    assert len(pdf) < 500_000
    assert pdf.count(b"/Type /Page /Parent") == len(pages) + 1
    # The PNG ZIP carries the same stored lettering, drawn over the unchanged provider art.
    zipped = api_client.post(f"/api/v1/projects/{project_id}/export", json={"format": "png"})
    assert zipped.status_code == 202, zipped.text
    assert not zipped.json().get("warnings")
    with zipfile.ZipFile(BytesIO(httpx.get(zipped.json()["export"]["file_url"]).content)) as z:
        manifest = json.loads(z.read("manifest.json"))
        assert [p["lettering"] for p in manifest["pages"]] == ["drawn"] * len(pages)
        lettered = Image.open(BytesIO(z.read("pages/page-001.png"))).convert("RGB")
        assert lettered.size == (256, 384)
        assert z.read(manifest["pages"][0]["source_file"]) == image
        assert lettered.tobytes() != Image.open(BytesIO(image)).convert("RGB").tobytes()
    invalid = api_client.patch(override_url, json={"override": {"center": {"x": -0.5, "y": -0.5}}})
    assert invalid.status_code == 200, invalid.text
    flagged = api_client.post(f"/api/v1/projects/{project_id}/export")
    assert flagged.status_code == 202, flagged.text
    warnings = flagged.json()["warnings"]
    assert warnings[0]["code"] == "lettering_review_required"
    assert "out_of_panel" in warnings[0]["flags"]
    note_url = flagged.json()["export"]["file_url"]
    review = httpx.get(note_url).content
    note = subprocess.run(["pdftotext", "-", "-"], input=review, capture_output=True, check=True).stdout.decode()
    assert "LETTERING NOT EXPORTED" in note and "out_of_panel" in note
    # A flagged layout is withheld from the PNG ZIP too, with the same typed warning.
    zipped = api_client.post(f"/api/v1/projects/{project_id}/export", json={"format": "png"})
    assert zipped.status_code == 202, zipped.text
    assert zipped.json()["warnings"][0]["code"] == "lettering_review_required"
    with zipfile.ZipFile(BytesIO(httpx.get(zipped.json()["export"]["file_url"]).content)) as z:
        assert json.loads(z.read("manifest.json"))["pages"][0]["lettering"] == "withheld"
        assert z.read("pages/page-001.png") == image
    # The retired Ink variant is refused and never consumes text-free art.
    assert (
        api_client.post(f"/api/v1/pages/{page_id}/line-art", json={"variant": "ink"}).status_code
        == 400
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


@pytest.mark.parametrize(
    ("warning_payload", "export_status", "should_pass", "expected_err"),
    [
        (
            [
                {
                    "code": "lettering_review_required",
                    "flags": ["no_calm_zone"],
                    "message": "Some lettering was withheld because no calm zone was found.",
                    "page_number": 1,
                }
            ],
            "completed",
            True,
            None,
        ),
        (
            [
                {
                    "code": "unauthorized_code",
                    "flags": ["no_calm_zone"],
                    "message": "Unauthorized warning code.",
                    "page_number": 1,
                }
            ],
            "completed",
            False,
            "overlay export warnings",
        ),
        (
            [
                {
                    "code": "lettering_review_required",
                    "flags": ["out_of_panel"],
                    "message": "Out of panel.",
                    "page_number": 1,
                }
            ],
            "completed",
            False,
            "overlay export warnings",
        ),
        (
            [
                {
                    "code": "lettering_review_required",
                    "flags": ["no_calm_zone", "out_of_panel"],
                    "message": "Multiple flags.",
                    "page_number": 1,
                }
            ],
            "completed",
            False,
            "overlay export warnings",
        ),
        (
            [
                {
                    "code": "lettering_review_required",
                    "flags": [],
                    "message": "Empty flags.",
                    "page_number": 1,
                }
            ],
            "completed",
            False,
            "overlay export warnings",
        ),
        (
            [
                {
                    "code": "lettering_review_required",
                    "flags": ["no_calm_zone"],
                    "message": "Some lettering was withheld because no calm zone was found.",
                    "page_number": 1,
                }
            ],
            "failed",
            False,
            "export did not complete: failed",
        ),
    ],
)
def test_comic_e2e_fake_transport_export_warning_handling(
    monkeypatch, tmp_path, capsys, warning_payload, export_status, should_pass, expected_err
):
    from scripts.e2e import comic_e2e as runner

    content = b"BT /F1 12 Tf 72 712 Td (Hello world) Tj ET"
    stream_len = len(content)
    pdf_bytes = (
        b"%PDF-1.4\n"
        b"1 0 obj << /Type /Catalog /Pages 2 0 R >> endobj\n"
        b"2 0 obj << /Type /Pages /Kids [3 0 R 4 0 R] /Count 2 >> endobj\n"
        b"3 0 obj << /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] >> endobj\n"
        b"4 0 obj << /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Contents 5 0 R /Resources << /Font << /F1 6 0 R >> >> >> endobj\n"
        b"5 0 obj << /Length " + str(stream_len).encode() + b" >>\n"
        b"stream\n" + content + b"\nendstream\nendobj\n"
        b"6 0 obj << /Type /Font /Subtype /Type1 /BaseFont /Helvetica >> endobj\n"
        b"xref\n0 7\n"
        b"0000000000 65535 f \n"
        b"0000000009 00000 n \n"
        b"0000000058 00000 n \n"
        b"0000000123 00000 n \n"
        b"0000000202 00000 n \n"
        b"0000000320 00000 n \n"
        b"0000000410 00000 n \n"
        b"trailer << /Size 7 /Root 1 0 R >>\n"
        b"startxref\n480\n%%EOF\n"
    )

    panels = [{"id": f"pan-{i}", "dialogue": "hi"} for i in range(4)]
    pages = [
        {
            "id": "page-1",
            "lettering_mode": "overlay",
            "panel_ids": [f"pan-{i}" for i in range(4)],
            "draft_image_url": "http://127.0.0.1:8080/media/draft.png",
            "draft_approved_at": "2026-01-01T00:00:00Z",
            "painted_page_image_url": "http://127.0.0.1:8080/media/painted.png",
            "painted_page_approved_at": "2026-01-01T00:00:00Z",
        }
    ]

    class FakeTransport:
        def __init__(self):
            self.download_called = False

        def __call__(self, path: str, method: str = "GET", payload=None, extra_headers=None):
            if path == "/healthz":
                return {}
            if path == "/api/v1/me/credit-usage":
                return {"balance": 1000}
            if path == "/api/v1/projects" and method == "POST":
                return {"project": {"id": "proj-1", "finish_preset": "off"}}
            if path == "/api/v1/projects/proj-1/characters":
                return {"characters": [{"id": "char-1", "name": "Nia"}]}
            if path == "/api/v1/characters/char-1/reference-image" and method == "POST":
                return {}
            if path == "/api/v1/characters/char-1/approve" and method == "POST":
                return {}
            if path == "/api/v1/projects/proj-1/chapters" and method == "GET":
                return {"chapters": [{"id": "chap-1"}]}
            if path == "/api/v1/projects/proj-1/chapters" and method == "POST":
                return {"chapter": {"id": "chap-1"}}
            if path == "/api/v1/chapters/chap-1/approve" and method == "POST":
                return {}
            if path == "/api/v1/chapters/chap-1/panels":
                return {"panels": panels}
            if path == "/api/v1/chapters/chap-1/pages":
                return {"pages": pages}
            if path == "/api/v1/panels/pan-0" and method == "PATCH":
                return {}
            if path == "/api/v1/pages/page-1/draft" and method == "POST":
                return {}
            if path == "/api/v1/pages/page-1/draft/approve" and method == "POST":
                return {}
            if path == "/api/v1/pages/page-1/painted-page" and method == "POST":
                return {}
            if path == "/api/v1/pages/page-1/line-art/approve" and method == "POST":
                return {}
            if path == "/api/v1/pages/page-1/lettering":
                return {
                    "lettering": {
                        "version": "page-lettering-v1",
                        "input": {
                            "panels": [
                                {
                                    "panel_id": "pan-0",
                                    "utterances": [
                                        {
                                            "utterance_id": "u-1",
                                            "kind": "speech",
                                            "text": "Hello world",
                                        }
                                    ],
                                }
                            ]
                        },
                        "layout": {
                            "bubbles": [
                                {"lines": [{"text": "Hello world"}], "flags": []},
                                {"lines": [{"text": "Withheld"}], "flags": ["no_calm_zone"]},
                            ]
                        },
                        "overrides": {},
                    }
                }
            if path == "/api/v1/pages/page-1/lettering/utterances/u-1/override" and method == "PATCH":
                return {
                    "lettering": {
                        "layout": {
                            "bubbles": [
                                {"lines": [{"text": "Hello world"}], "flags": []},
                                {"lines": [{"text": "Withheld"}], "flags": ["no_calm_zone"]},
                            ],
                            "modified": True,
                        },
                        "overrides": {"u-1": {"center": {"x": 0.5, "y": 0.3}}},
                    }
                }
            if path == "/api/v1/projects/proj-1/export" and method == "POST":
                resp = {"export": {"id": "exp-1"}}
                if warning_payload is not None:
                    resp["warnings"] = warning_payload
                return resp
            if path == "/api/v1/exports/exp-1":
                return {"export": {"id": "exp-1", "status": export_status}}
            if path == "/api/v1/exports/exp-1/download":
                self.download_called = True
                return pdf_bytes
            raise AssertionError(f"unexpected request: {method} {path}")

    fake_transport = FakeTransport()
    monkeypatch.setattr(runner, "json_request", fake_transport)
    monkeypatch.setattr(runner, "fetch_line_art_image", lambda url: b"fake-png-bytes")
    monkeypatch.setattr(
        runner, "provision_run_user", lambda: "00000000-0000-0000-0000-000000000001"
    )

    guard = runner.CostGuard(float("inf"), False)
    guard.overlay_lettering = True
    guard.finish_preset = "off"

    if should_pass:
        count, project_id = runner.run_flow(guard, tmp_path)
        assert project_id == "proj-1" and count == 1
        assert fake_transport.download_called is True
        assert (tmp_path / "latest.pdf").exists()
        assert (tmp_path / "latest.pdf").read_bytes() == pdf_bytes
        captured = capsys.readouterr()
        assert "WARNING overlay export: lettering_review_required" in captured.out
        assert "no_calm_zone" in captured.out
    else:
        with pytest.raises(runner.E2EError, match=expected_err):
            runner.run_flow(guard, tmp_path)
        assert fake_transport.download_called is False
        assert not (tmp_path / "latest.pdf").exists()

