"""Boundary contract tests verifying FastAPI endpoints conform to contracts/openapi/ai-service.yaml."""

from fastapi.testclient import TestClient

from app.gen.models import AnalyzeScriptRequest, HealthResponse
from app.main import create_app

client = TestClient(create_app())


def test_healthz():
    response = client.get("/healthz")
    assert response.status_code == 200
    data = response.json()
    assert data == {"status": "ok"}
    health = HealthResponse(**data)
    assert health.status == "ok"


def test_contract_models():
    req = AnalyzeScriptRequest(
        job_id="11111111-1111-1111-1111-111111111111",
        hashed_user_id="user-hash-abc",
        project_id="22222222-2222-2222-2222-222222222222",
        script="A scientist activates a quantum portal.",
        target_chapter_count=3,
    )
    assert req.target_chapter_count == 3
