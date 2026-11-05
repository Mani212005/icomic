"""Reconstructed historical shapes through real services, also via replaying fakes."""

import base64
import json
from pathlib import Path
from uuid import uuid4

import httpx
import pytest
from fastapi.testclient import TestClient

from app.adapters.factory import build_adapters_from_env
from app.adapters.image.fake import FakeImageAdapter
from app.adapters.image.meta import MetaImageAdapter
from app.adapters.language.fake import FakeLanguageAdapter
from app.adapters.language.meta import MetaLanguageAdapter
from app.adapters.vision.fake import FakeVisionAdapter
from app.adapters.vision.meta import MetaVisionAdapter
from app.main import create_app
from app.provider_costs import ProviderCostLedger
from app.provider_diagnostics import ProviderDiagnostics

FIXTURES = Path(__file__).resolve().parents[1] / "fixtures/meta/observed"


def fixture(name):
    return json.loads((FIXTURES / f"{name}.json").read_text())


def wired_replay(tmp_path, name, fake):
    record = fixture(name)
    transmitted = []

    def respond(request):
        if request.url.path.endswith("/responses/input_tokens"):
            return httpx.Response(200, json={"input_tokens": 10000})
        transmitted.append(request.url.path)
        return httpx.Response(record["status"], json=record["body"])

    ledger = ProviderCostLedger(2, path=tmp_path / "costs.json")
    diagnostics = ProviderDiagnostics(tmp_path / "diagnostics")
    kwargs = {
        "api_key": "hermetic-replay",
        "base_url": "http://mock.meta.internal/v1",
        "transport": httpx.MockTransport(respond),
        "cost_ledger": ledger,
        "diagnostics": diagnostics,
    }
    adapters = build_adapters_from_env()
    language = MetaLanguageAdapter(**kwargs)
    vision = MetaVisionAdapter(**kwargs, cost_stage="vision")
    image = MetaImageAdapter(**kwargs, cost_stage="image")
    adapters.language = FakeLanguageAdapter(replay_adapter=language) if fake else language
    adapters.vision = FakeVisionAdapter(replay_adapter=vision) if fake else vision
    adapters.image = FakeImageAdapter(replay_adapter=image) if fake else image
    adapters.cost_ledger = ledger
    adapters.diagnostics = diagnostics
    raw = base64.b64decode(fixture("webp_png_label")["body"]["output"][0]["result"])
    image_url = adapters.storage.put("source.png", raw, "image/png")
    return adapters, transmitted, image_url


def request_for(operation, image_url):
    common = {
        "job_id": str(uuid4()),
        "project_id": str(uuid4()),
        "hashed_user_id": "synthetic-user",
    }
    inputs = {
        "moderate-prompt": {"prompt": "A safe synthetic scene."},
        "analyze-script": {"script": "A safe synthetic scene."},
        "generate-chapter-outline": {
            "script": "A safe synthetic scene.",
            "chapter_count": 1,
            "approved_characters": [],
        },
        "generate-panel-text": {
            "chapter_id": str(uuid4()),
            "chapter_number": 1,
            "chapter_title": "Scene",
            "chapter_summary": "A safe synthetic scene.",
            "characters": [],
        },
        "generate-character-reference": {
            "character_id": str(uuid4()),
            "character_name": "Hero",
            "prompt": "An explorer",
            "style": "storybook",
        },
        "suggest-colour-palette": {
            "page_id": str(uuid4()),
            "line_art_image_url": image_url,
            "panel_context": "Sea",
        },
    }
    return {**common, **inputs[operation]}


@pytest.mark.parametrize("fake", [False, True], ids=["meta-transport", "fake-replay"])
@pytest.mark.parametrize(
    "operation,name,expected",
    [
        ("analyze-script", "schema_echo", "INVALID_PROVIDER_OUTPUT"),
        ("analyze-script", "characters_object", "INVALID_PROVIDER_OUTPUT"),
        ("generate-chapter-outline", "outline_list", "INVALID_PROVIDER_OUTPUT"),
        ("generate-chapter-outline", "schema_echo", "INVALID_PROVIDER_OUTPUT"),
        ("generate-panel-text", "schema_echo", "INVALID_PROVIDER_OUTPUT"),
        ("analyze-script", "empty_length", "INVALID_PROVIDER_OUTPUT"),
        ("generate-panel-text", "truncated_json", "INVALID_PROVIDER_OUTPUT"),
        ("generate-panel-text", "invalid_json", "INVALID_PROVIDER_OUTPUT"),
        ("moderate-prompt", "http_404", "UPSTREAM_ERROR"),
        ("moderate-prompt", "moderation_length_873", "INVALID_PROVIDER_OUTPUT"),
        ("generate-panel-text", "http_400", "UPSTREAM_ERROR"),
        ("suggest-colour-palette", "palette_schema_echo", "INVALID_PROVIDER_OUTPUT"),
        ("suggest-colour-palette", "palette_list", "INVALID_PROVIDER_OUTPUT"),
        ("suggest-colour-palette", "palette_invalid_json", "INVALID_PROVIDER_OUTPUT"),
        ("generate-character-reference", "invalid_image", "INVALID_PROVIDER_OUTPUT"),
    ],
)
def test_failed_shapes_stop_retain_usage_and_do_not_publish(
    tmp_path, operation, name, expected, fake
):
    adapters, calls, image_url = wired_replay(tmp_path, name, fake)
    before = dict(adapters.storage._blobs)
    client = TestClient(create_app(adapters))
    request = request_for(operation, image_url)
    response = client.post("/internal/v1/" + operation, json=request)
    assert response.status_code == 502, response.text
    assert response.json()["code"] == expected
    assert response.json()["retryable"] is False
    assert response.json()["retry_after_seconds"] is None
    assert "raw provider" not in response.text
    status = client.get(f"/internal/v1/job/{request['job_id']}/status").json()
    report = adapters.cost_ledger.report()
    assert status["status"] == "failed"
    assert status["error_code"] == expected
    assert len(report["calls"]) == 1
    assert status["actual_cost"] == pytest.approx(report["actual_spend_usd"])
    if name.startswith("http_"):
        assert report["unpriced_calls"] == 1
        assert report["accounted_spend_usd"] > 0
    else:
        assert report["actual_spend_usd"] > 0
        assert report["unpriced_calls"] == 0
    assert adapters.storage._blobs == before
    saved = adapters.queue.get_job("generation", request["job_id"])
    assert "response" not in saved
    diagnostics = list((tmp_path / "diagnostics").glob("*.json"))
    assert len(diagnostics) == 1
    body = json.loads(diagnostics[0].read_text())
    assert json.loads(body["body"]) == fixture(name)["body"]
    assert body["job_id"] == request["job_id"]
    assert (
        client.post("/internal/v1/" + operation, json=request).status_code == response.status_code
    )
    assert len(calls) == 1


@pytest.mark.parametrize("fake", [False, True])
@pytest.mark.parametrize(
    "operation,name",
    [
        ("moderate-prompt", "moderation"),
        ("analyze-script", "characters_cached"),
        ("suggest-colour-palette", "vision"),
        ("generate-character-reference", "webp_png_label"),
    ],
)
def test_valid_observed_shapes_are_preserved(tmp_path, operation, name, fake):
    adapters, calls, image_url = wired_replay(tmp_path, name, fake)
    client = TestClient(create_app(adapters))
    response = client.post("/internal/v1/" + operation, json=request_for(operation, image_url))
    assert response.status_code == 200, response.text
    assert len(calls) == 1
    assert adapters.cost_ledger.report()["actual_spend_usd"] > 0
    if name == "webp_png_label":
        url = response.json()["image_url"]
        assert url.endswith(".webp")
        assert adapters.storage.get_content_type(url) == "image/webp"
        assert adapters.storage.get(url) == base64.b64decode(
            fixture(name)["body"]["output"][0]["result"]
        )
    assert not list((tmp_path / "diagnostics").glob("*.json"))
