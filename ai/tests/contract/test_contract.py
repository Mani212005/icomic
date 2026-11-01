"""Boundary contract tests verifying FastAPI endpoints conform to contracts/openapi/ai-service.yaml."""

from fastapi.testclient import TestClient

from app.main import create_app

client = TestClient(create_app())


def test_healthz():
    response = client.get("/healthz")
    assert response.status_code == 200
    assert response.json() == {"status": "ok"}


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
    assert data["job_id"] == payload["job_id"]
    assert "character_profiles" in data
    assert len(data["character_profiles"]) > 0
    assert data["suggested_chapter_count"] == 3
    assert data["word_count"] > 0


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
    assert data["job_id"] == payload["job_id"]
    assert "image_url" in data
    assert "provenance_metadata" in data
    assert data["provenance_metadata"]["c2pa"]["watermark_present"] is True


def test_generate_draft_sketch_contract():
    payload = {
        "job_id": "55555555-5555-5555-5555-555555555555",
        "hashed_user_id": "user-hash-abc",
        "project_id": "22222222-2222-2222-2222-222222222222",
        "page_id": "66666666-6666-6666-6666-666666666666",
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
    }
    response = client.post("/internal/v1/generate-draft-sketch", json=payload)
    assert response.status_code == 200
    data = response.json()
    assert data["job_id"] == payload["job_id"]
    assert "image_url" in data
    assert "model_id" in data


def test_moderate_prompt_contract():
    payload = {
        "job_id": "77777777-7777-7777-7777-777777777777",
        "hashed_user_id": "user-hash-abc",
        "prompt": "A heroic speech at the summit",
    }
    response = client.post("/internal/v1/moderate-prompt", json=payload)
    assert response.status_code == 200
    data = response.json()
    assert data["job_id"] == payload["job_id"]
    assert data["blocked"] is False


def test_assemble_pdf_contract():
    payload = {
        "job_id": "88888888-8888-8888-8888-888888888888",
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
            }
        ],
    }
    response = client.post("/internal/v1/assemble-pdf", json=payload)
    assert response.status_code == 200
    data = response.json()
    assert data["pdf_url"].endswith(".pdf")
    assert data["provenance_metadata_preserved"] is True
    assert data["page_count"] == 1


def test_job_status_contract():
    response = client.get("/internal/v1/job/random-job-id/status")
    assert response.status_code == 200
    data = response.json()
    assert data["job_id"] == "random-job-id"
    assert data["status"] == "pending"
