"""Boundary contract tests verifying FastAPI endpoints conform to contracts/openapi/ai-service.yaml."""

from fastapi.testclient import TestClient

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

app = create_app()
client = TestClient(app)


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
    assert model.panels[0].dialogue != ""
    assert model.panels[0].image_prompt != ""


def test_generate_draft_sketch_contract():
    app.state.storage_adapter.put("char1.png", b"\x89PNG\r\n\x1a\nreference", "image/png")
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
    app.state.storage_adapter.put("draft_1.png", b"\x89PNG\r\n\x1a\ndraft", "image/png")
    app.state.storage_adapter.put("char1.png", b"\x89PNG\r\n\x1a\nreference", "image/png")
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


def test_assemble_pdf_contract():
    payload = {
        "job_id": "dddddddd-dddd-dddd-dddd-dddddddddddd",
        "hashed_user_id": "user-hash-abc",
        "project_id": "22222222-2222-2222-2222-222222222222",
        "title": "Quantum Odyssey",
        "page_size": "A4",
        "include_ai_disclosure": True,
        "pages": [
            {
                "page_number": 1,
                "image_url": "http://storage/page1.png",
                "panel_count": 4,
                "provenance_metadata": {"c2pa": True},
            }
        ],
    }
    response = client.post("/internal/v1/assemble-pdf", json=payload)
    assert response.status_code == 200
    data = response.json()
    model = AssemblePdfResponse(**data)
    assert model.job_id == payload["job_id"]
    assert model.pdf_url.endswith(".pdf")
    assert model.provenance_metadata_preserved is True
    # 1 cover page + 1 comic page = 2 pages total
    assert model.page_count == 2


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
