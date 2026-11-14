"""Boundary contract tests verifying FastAPI endpoints conform to contracts/openapi/ai-service.yaml."""

import base64
import hashlib
import json
import zipfile
from io import BytesIO
from pathlib import Path

from fastapi.testclient import TestClient
from PIL import Image

from app.gen.models import (
    AnalyzeScriptResponse,
    AssemblePdfResponse,
    ErrorResponse,
    GenerateChapterOutlineResponse,
    GenerateCharacterReferenceResponse,
    GenerateDraftSketchResponse,
    GenerateLineArtResponse,
    GeneratePanelTextResponse,
    HealthResponse,
    JobStatusResponse,
    ModeratePromptResponse,
    SuggestColourPaletteResponse,
)
from app.main import create_app
from tests.image_fixtures import image_bytes

app = create_app()
client = TestClient(app)


def test_store_colour_image_contract_and_content_type_validation():
    page_id = "55555555-5555-5555-5555-555555555555"
    png_buffer = BytesIO()
    Image.new("RGB", (2, 2), "red").save(png_buffer, format="PNG")
    png_bytes = png_buffer.getvalue()
    response = client.post(
        "/internal/v1/store-colour-image",
        json={
            "page_id": page_id,
            "image_base64": base64.b64encode(png_bytes).decode("ascii"),
            "media_type": "image/png",
        },
    )
    assert response.status_code == 200
    image_url = response.json()["image_url"]
    assert f"pages/{page_id}/colours/" in image_url
    assert app.state.storage_adapter.get(image_url) == png_bytes
    assert app.state.storage_adapter.get_content_type(image_url) == "image/png"

    invalid = client.post(
        "/internal/v1/store-colour-image",
        json={
            "page_id": page_id,
            "image_base64": base64.b64encode(b"not an image").decode("ascii"),
            "media_type": "image/png",
        },
    )
    assert invalid.status_code == 400

    mismatched = client.post(
        "/internal/v1/store-colour-image",
        json={
            "page_id": page_id,
            "image_base64": base64.b64encode(png_bytes).decode("ascii"),
            "media_type": "image/webp",
        },
    )
    assert mismatched.status_code == 400


def test_healthz():
    response = client.get("/healthz")
    assert response.status_code == 200
    data = response.json()
    assert data == {"status": "ok"}
    health = HealthResponse(**data)
    assert health.status == "ok"


def test_analyze_script_contract():
    payload = {
        "job_id": "11111111-1111-1111-1111-111111111111",
        "hashed_user_id": "user-hash-abc",
        "project_id": "22222222-2222-2222-2222-222222222222",
        "script": "A scientist accidentally activates an ancient quantum portal in her lab.",
        "target_chapter_count": 3,
    }
    response = client.post("/internal/v1/analyze-script", json=payload)
    assert response.status_code == 200
    data = response.json()
    model = AnalyzeScriptResponse(**data)
    assert model.job_id == payload["job_id"]
    assert len(model.character_profiles) > 0
    assert model.suggested_chapter_count == 3
    assert model.word_count > 0
    assert isinstance(model.warnings, list)


def test_generate_character_reference_contract():
    payload = {
        "job_id": "33333333-3333-3333-3333-333333333333",
        "hashed_user_id": "user-hash-abc",
        "project_id": "22222222-2222-2222-2222-222222222222",
        "character_id": "44444444-4444-4444-4444-444444444444",
        "character_name": "Dr. Sarah",
        "prompt": "Scientist in white lab coat with goggles",
        "style": "classic-comic",
    }
    response = client.post("/internal/v1/generate-character-reference", json=payload)
    assert response.status_code == 200
    data = response.json()
    model = GenerateCharacterReferenceResponse(**data)
    assert model.job_id == payload["job_id"]
    assert model.image_url.startswith("http")
    assert model.model_id == "muse-image-1.0-fake"
    assert model.provenance_metadata["c2pa"]["watermark_present"] is True


def test_generate_chapter_outline_contract():
    payload = {
        "job_id": "44444444-4444-4444-4444-444444444444",
        "hashed_user_id": "user-hash-abc",
        "project_id": "22222222-2222-2222-2222-222222222222",
        "script": "The hero enters the dark cave looking for the lost artifact.",
        "chapter_count": 2,
        "approved_characters": [
            {
                "name": "Maya",
                "description": "Young explorer",
                "age_range": "20-25",
                "appearance": "Short hair, leather jacket",
                "hair_style": "Pixie",
                "clothing": "Jacket and boots",
                "personality": "Fearless",
                "visual_attributes": {"tone": "warm"},
            }
        ],
    }
    response = client.post("/internal/v1/generate-chapter-outline", json=payload)
    assert response.status_code == 200
    data = response.json()
    model = GenerateChapterOutlineResponse(**data)
    assert model.job_id == payload["job_id"]
    assert len(model.chapters) == 2
    assert model.chapters[0].chapter_number == 1
    assert "Maya" in model.chapters[0].character_names


def test_generate_panel_text_contract():
    payload = {
        "job_id": "55555555-5555-5555-5555-555555555555",
        "hashed_user_id": "user-hash-abc",
        "project_id": "22222222-2222-2222-2222-222222222222",
        "chapter_id": "66666666-6666-6666-6666-666666666666",
        "chapter_number": 1,
        "chapter_title": "The Awakening",
        "chapter_summary": "The ancient portal powers on.",
        "characters": ["Dr. Sarah"],
    }
    response = client.post("/internal/v1/generate-panel-text", json=payload)
    assert response.status_code == 200
    data = response.json()
    model = GeneratePanelTextResponse(**data)
    assert model.job_id == payload["job_id"]
    assert len(model.panels) == 4
    assert model.panels[0].scene_description != ""
    assert model.panels[0].image_prompt != ""


def test_generate_panel_text_accepts_the_approved_outline_detail():
    payload = {
        "job_id": "55555555-5555-5555-5555-555555555556",
        "hashed_user_id": "user-hash-abc",
        "project_id": "22222222-2222-2222-2222-222222222222",
        "chapter_id": "66666666-6666-6666-6666-666666666666",
        "chapter_number": 1,
        "chapter_title": "The Awakening",
        "chapter_summary": "The ancient portal powers on.",
        "characters": ["Dr. Sarah"],
        "important_events": ["The portal hums", "A door opens"],
        "character_names": ["Dr. Sarah"],
        "ending": "She steps through",
    }
    response = client.post("/internal/v1/generate-panel-text", json=payload)
    assert response.status_code == 200
    assert len(GeneratePanelTextResponse(**response.json()).panels) == 4
    response = client.post(
        "/internal/v1/generate-panel-text", json={**payload, "important_events": "one"}
    )
    assert response.status_code == 400
    assert "important_events" in response.text


def test_generate_draft_sketch_contract():
    app.state.storage_adapter.put("char1.png", image_bytes("reference"), "image/png")
    payload = {
        "job_id": "77777777-7777-7777-7777-777777777777",
        "hashed_user_id": "user-hash-abc",
        "project_id": "22222222-2222-2222-2222-222222222222",
        "page_id": "88888888-8888-8888-8888-888888888888",
        "page_number": 1,
        "style": "noir",
        "panels": [
            {
                "panel_number": 1,
                "scene_description": "Lab console explodes",
                "character_actions": "Shields face",
                "camera_angle": "Close up",
                "background": "Sparks flying",
                "dialogue": "It is overloading!",
                "narration": "Silence fell",
                "image_prompt": "Comic panel 1",
            }
        ],
        "character_reference_image_urls": ["http://storage/char1.png"],
    }
    response = client.post("/internal/v1/generate-draft-sketch", json=payload)
    assert response.status_code == 200
    data = response.json()
    model = GenerateDraftSketchResponse(**data)
    assert model.job_id == payload["job_id"]
    assert model.image_url.startswith("http")
    assert model.model_id == "muse-image-1.0-fake"


def test_generate_line_art_contract():
    app.state.storage_adapter.put(
        "draft_1.png", image_bytes("89504e470d0a1a0a6472616674"), "image/png"
    )
    app.state.storage_adapter.put("char1.png", image_bytes("reference"), "image/png")
    payload = {
        "job_id": "99999999-9999-9999-9999-999999999999",
        "hashed_user_id": "user-hash-abc",
        "project_id": "22222222-2222-2222-2222-222222222222",
        "page_id": "88888888-8888-8888-8888-888888888888",
        "page_number": 1,
        "draft_image_url": "http://storage/draft_1.png",
        "style": "noir",
        "panels": [
            {
                "panel_number": 1,
                "scene_description": "Lab console explodes",
                "character_actions": "Shields face",
                "camera_angle": "Close up",
                "background": "Sparks flying",
                "dialogue": "It is overloading!",
                "narration": "Silence fell",
                "image_prompt": "Comic panel 1",
            }
        ],
        "character_reference_image_urls": ["http://storage/char1.png"],
    }
    response = client.post("/internal/v1/generate-line-art", json=payload)
    assert response.status_code == 200
    data = response.json()
    model = GenerateLineArtResponse(**data)
    assert model.job_id == payload["job_id"]
    assert model.image_url.startswith("http")
    assert model.model_id == "muse-image-1.0-fake"


def test_suggest_colour_palette_contract():
    line_art_url = app.state.storage_adapter.put(
        "line-art/line_art_1.png", b"approved line art bytes", "image/png"
    )
    payload = {
        "job_id": "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa",
        "hashed_user_id": "user-hash-abc",
        "project_id": "22222222-2222-2222-2222-222222222222",
        "page_id": "88888888-8888-8888-8888-888888888888",
        "line_art_image_url": line_art_url,
        "panel_context": "Nighttime neon alleyway in cyberpunk city",
    }
    response = client.post("/internal/v1/suggest-colour-palette", json=payload)
    assert response.status_code == 200
    data = response.json()
    model = SuggestColourPaletteResponse(**data)
    assert model.job_id == payload["job_id"]
    assert model.theme != ""
    assert len(model.palette) > 0
    for colour in model.palette:
        assert colour.hex_color.startswith("#")
        assert len(colour.hex_color) == 7


def test_moderate_prompt_contract():
    payload = {
        "job_id": "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb",
        "hashed_user_id": "user-hash-abc",
        "prompt": "A heroic speech at the summit",
    }
    response = client.post("/internal/v1/moderate-prompt", json=payload)
    assert response.status_code == 200
    data = response.json()
    model = ModeratePromptResponse(**data)
    assert model.job_id == payload["job_id"]
    assert model.blocked is False
    assert model.reason is None


def test_moderate_prompt_contract_blocked():
    payload = {
        "job_id": "cccccccc-cccc-cccc-cccc-cccccccccccc",
        "hashed_user_id": "user-hash-abc",
        "prompt": "An illegal extreme violence act",
    }
    response = client.post("/internal/v1/moderate-prompt", json=payload)
    assert response.status_code == 200
    data = response.json()
    model = ModeratePromptResponse(**data)
    assert model.job_id == payload["job_id"]
    assert model.blocked is True
    assert model.reason is not None


def _stored_page(name, label):
    art = image_bytes(label, "PNG")
    url = app.state.storage_adapter.put(f"pages/{name}.png", art, "image/png")
    return url, {"source_hash": hashlib.sha256(art).hexdigest()}, art


def _assemble_payload(job_id, pages, **extra):
    return {
        "job_id": job_id,
        "hashed_user_id": "user-hash-abc",
        "project_id": "22222222-2222-2222-2222-222222222222",
        "title": "Quantum Odyssey",
        "page_size": "A4",
        "include_ai_disclosure": True,
        "pages": pages,
        **extra,
    }


def test_assemble_pdf_contract():
    url, provenance, _ = _stored_page("contract-page-1", "contract-pdf")
    payload = _assemble_payload(
        "dddddddd-dddd-dddd-dddd-dddddddddddd",
        [{"page_number": 1, "image_url": url, "panel_count": 4, "provenance_metadata": provenance}],
    )
    response = client.post("/internal/v1/assemble-pdf", json=payload)
    assert response.status_code == 200
    data = response.json()
    model = AssemblePdfResponse(**data)
    assert model.job_id == payload["job_id"]
    assert model.pdf_url.endswith(".pdf")
    assert model.provenance_metadata_preserved is True
    # 1 cover page + 1 comic page = 2 pages total
    assert model.page_count == 2


def test_assemble_pdf_contract_reports_unproven_provenance_as_not_preserved():
    url, _, _ = _stored_page("contract-page-unproven", "contract-unproven")
    payload = _assemble_payload(
        "dddddddd-dddd-dddd-dddd-ddddddddddd1",
        [{"page_number": 1, "image_url": url, "provenance_metadata": {"c2pa": True}}],
    )
    response = client.post("/internal/v1/assemble-pdf", json=payload)
    assert response.status_code == 200
    assert response.json()["provenance_metadata_preserved"] is False


def test_assemble_pdf_contract_rejects_missing_art_instead_of_drawing_a_placeholder():
    payload = _assemble_payload(
        "dddddddd-dddd-dddd-dddd-ddddddddddd2",
        [{"page_number": 1, "image_url": "http://storage/never-stored.png"}],
    )
    response = client.post("/internal/v1/assemble-pdf", json=payload)
    assert response.status_code == 409
    assert response.json()["code"] == "EXPORT_INCOMPLETE"
    assert "Page 1" in response.text and "no longer in storage" in response.text


def test_assemble_png_zip_contract_bundles_pages_sources_and_manifest():
    url_1, provenance_1, art_1 = _stored_page("contract-zip-1", "contract-zip-1")
    webp = image_bytes("contract-zip-2", "WEBP")
    url_2 = app.state.storage_adapter.put("pages/contract-zip-2.webp", webp, "image/webp")
    payload = _assemble_payload(
        "dddddddd-dddd-dddd-dddd-ddddddddddd3",
        [
            {"page_number": 1, "image_url": url_1, "provenance_metadata": provenance_1},
            {
                "page_number": 2,
                "image_url": url_2,
                "provenance_metadata": {"source_hash": hashlib.sha256(webp).hexdigest()},
            },
        ],
        format="png_zip",
        page_size="US_Letter",
    )
    response = client.post("/internal/v1/assemble-pdf", json=payload)
    assert response.status_code == 200, response.text
    model = AssemblePdfResponse(**response.json())
    assert model.pdf_url.endswith("comic-pages.zip")
    assert model.provenance_metadata_preserved is True
    assert model.page_count == 2

    archive_bytes = app.state.storage_adapter.get(model.pdf_url)
    assert archive_bytes[:4] == b"PK\x03\x04"
    assert app.state.storage_adapter.get_content_type(model.pdf_url) == "application/zip"
    with zipfile.ZipFile(BytesIO(archive_bytes)) as archive:
        assert archive.namelist() == [
            "pages/page-001.png",
            "pages/page-002.png",
            "sources/page-002-source.webp",
            "manifest.json",
            "DISCLOSURE.txt",
        ]
        # A PNG source is the page, byte-identical; a WebP source is kept beside its PNG.
        assert archive.read("pages/page-001.png") == art_1
        assert archive.read("sources/page-002-source.webp") == webp
        for name in ("pages/page-001.png", "pages/page-002.png"):
            with Image.open(BytesIO(archive.read(name))) as page:
                assert page.format == "PNG" and page.size == (64, 64)
        manifest = json.loads(archive.read("manifest.json"))
        assert manifest["provenance_metadata_preserved"] is True
        assert [page["page_number"] for page in manifest["pages"]] == [1, 2]
        assert manifest["pages"][1]["source_sha256"] == hashlib.sha256(webp).hexdigest()
        assert "AI-generation disclosure" in archive.read("DISCLOSURE.txt").decode()


def _overlay_state():
    from app.gen.models import LetteringDocument
    from app.services.lettering import layout_page

    fixtures = Path(__file__).parents[1] / "fixtures/lettering"
    doc = LetteringDocument.model_validate_json((fixtures / "speech-tail.input.json").read_bytes())
    return {
        "version": "page-lettering-v1",
        "input": doc.model_dump(mode="json"),
        "overrides": {},
        "zone_overrides": {},
        "layout": layout_page(doc).model_dump(mode="json"),
    }


def _overlay_zip(job_id, name, **page):
    url, provenance, art = _stored_page(name, name)
    payload = _assemble_payload(
        job_id,
        [
            {
                "page_number": 1,
                "image_url": url,
                "provenance_metadata": provenance,
                "lettering_mode": "overlay",
                **page,
            }
        ],
        format="png_zip",
    )
    response = client.post("/internal/v1/assemble-pdf", json=payload)
    assert response.status_code == 200, response.text
    with zipfile.ZipFile(BytesIO(app.state.storage_adapter.get(response.json()["pdf_url"]))) as z:
        return (
            response.json(),
            art,
            z.read("pages/page-001.png"),
            json.loads(z.read("manifest.json")),
        )


def test_assemble_png_zip_contract_draws_stored_overlay_lettering():
    body, art, page, manifest = _overlay_zip(
        "dddddddd-dddd-dddd-dddd-ddddddddddd4", "contract-zip-overlay", lettering=_overlay_state()
    )
    assert body["warnings"] == []
    assert manifest["pages"][0]["lettering"] == "drawn"
    assert page != art  # the stored bubbles are in the page pixels
    with Image.open(BytesIO(page)) as lettered, Image.open(BytesIO(art)) as original:
        assert lettered.format == "PNG" and lettered.size == original.size


def test_assemble_png_zip_contract_warns_when_overlay_lettering_is_missing():
    body, art, page, manifest = _overlay_zip(
        "dddddddd-dddd-dddd-dddd-ddddddddddd5", "contract-zip-overlay-missing"
    )
    assert [w["code"] for w in body["warnings"]] == ["stale_lettering"]
    assert manifest["pages"][0]["lettering"] == "withheld"
    assert page == art


def test_job_status_contract():
    # 1. Check existing job created by previous test
    job_id = "11111111-1111-1111-1111-111111111111"
    response = client.get(f"/internal/v1/job/{job_id}/status")
    assert response.status_code == 200
    data = response.json()
    model = JobStatusResponse(**data)
    assert model.job_id == job_id
    assert model.status == "done"

    # 2. Check 404 for unknown job ID
    unknown_job_id = "00000000-0000-0000-0000-000000000000"
    missing_resp = client.get(f"/internal/v1/job/{unknown_job_id}/status")
    assert missing_resp.status_code == 404
    err_data = missing_resp.json()
    err_model = ErrorResponse(**err_data)
    assert err_model.code == "NOT_FOUND"


def test_validation_error_contract():
    # Send empty body to a required endpoint
    response = client.post("/internal/v1/analyze-script", json={})
    assert response.status_code == 400
    err_data = response.json()
    err_model = ErrorResponse(**err_data)
    assert err_model.code == "INVALID_ARGUMENT"
    assert "Invalid request" in err_model.message
