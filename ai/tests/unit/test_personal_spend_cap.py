"""The personal spend cap: durable, configuration-enabled, refused before transmission."""

import json
import logging
from pathlib import Path

import httpx
import pytest
from fastapi.testclient import TestClient

from app.adapters.factory import build_adapters_from_env, build_cost_ledger_from_env
from app.adapters.image.meta import MetaImageAdapter
from app.main import create_app
from app.provider_costs import (
    ProviderBudgetError,
    ProviderCostLedger,
    ProviderSpendCapError,
    provider_operation,
)
from app.services.errors import classify_error

IMAGE_RESPONSE = json.loads(
    (Path(__file__).parents[1] / "fixtures/meta/responses_image.json").read_text()
)


def _env(monkeypatch, tmp_path, cap="0.02"):
    for name in ("LANGUAGE", "IMAGE", "VISION", "QUEUE", "CLOCK", "STORAGE", "MEDIA_FETCH"):
        monkeypatch.setenv("ADAPTER_" + name, "fake")
    monkeypatch.delenv("ICOMIC_E2E_LIVE", raising=False)
    monkeypatch.delenv("ICOMIC_E2E_FAKE_STORAGE_DIR", raising=False)
    monkeypatch.setenv("PROVIDER_SPEND_CAP_USD", cap)
    monkeypatch.setenv("PROVIDER_COST_LEDGER_PATH", str(tmp_path / "provider-costs.json"))


def _image_adapter(ledger, sent):
    def respond(request):
        sent.append(request)
        return httpx.Response(200, json=IMAGE_RESPONSE)

    return MetaImageAdapter(
        api_key="mock-key",
        transport=httpx.MockTransport(respond),
        cost_ledger=ledger,
        cost_stage="image",
    )


def test_cap_refuses_the_call_that_would_pass_it_before_any_request_is_sent(tmp_path):
    ledger = ProviderCostLedger(0.02, path=tmp_path / "costs.json", run_id="personal", pinned=False)
    sent = []
    adapter = _image_adapter(ledger, sent)
    adapter.generate("A lighthouse", [], "user-hash", "storybook")
    adapter.generate("A harbour", [], "user-hash", "storybook")
    assert len(sent) == 2 and ledger.summary()["remaining_usd"] == 0
    with pytest.raises(ProviderSpendCapError):
        adapter.generate("A third page", [], "user-hash", "storybook")
    assert len(sent) == 2  # Refused before transmission: no third request existed.
    assert ledger.summary()["spent_usd"] == 0.02 and ledger.summary()["calls"] == 2


def test_spend_and_per_call_usage_survive_an_ai_restart(tmp_path):
    path = tmp_path / "costs.json"
    first = ProviderCostLedger(0.03, path=path, run_id="personal", pinned=False)
    token = provider_operation.set(("generate_painted_page", "job-1"))
    try:
        _image_adapter(first, []).generate("A lighthouse", [], "user-hash", "storybook")
    finally:
        provider_operation.reset(token)
    restarted = ProviderCostLedger(0.03, path=path, run_id="personal", pinned=False)
    summary = restarted.summary()
    assert (summary["spent_usd"], summary["remaining_usd"], summary["calls"]) == (0.01, 0.02, 1)
    call = json.loads(path.read_text())["calls"][0]
    assert call["job_id"] == "job-1" and call["operation"] == "generate_painted_page"
    assert call["status"] == "completed" and call["actual_cost_usd"] == 0.01
    assert "prompt" not in json.dumps(call).lower()


def test_cap_can_be_changed_between_restarts_without_forgetting_spend(tmp_path):
    path = tmp_path / "costs.json"
    ledger = ProviderCostLedger(0.01, path=path, run_id="personal", pinned=False)
    _image_adapter(ledger, []).generate("A lighthouse", [], "user-hash", "storybook")
    lowered = ProviderCostLedger(0.01, path=path, run_id="personal", pinned=False)
    with pytest.raises(ProviderSpendCapError):
        _image_adapter(lowered, []).generate("Again", [], "user-hash", "storybook")
    raised = ProviderCostLedger(0.02, path=path, run_id="personal", pinned=False)
    assert raised.summary()["spent_usd"] == 0.01 and raised.summary()["remaining_usd"] == 0.01
    _image_adapter(raised, []).generate("Again", [], "user-hash", "storybook")
    assert json.loads(path.read_text())["hard_cap_usd"] == 0.02


def test_a_pinned_e2e_ledger_still_rejects_a_changed_budget(tmp_path):
    path = tmp_path / "costs.json"
    ProviderCostLedger(0.5, path=path, run_id="run-a")
    with pytest.raises(ProviderBudgetError, match="different run or budget"):
        ProviderCostLedger(0.4, path=path, run_id="run-a")


def test_unfinished_calls_count_at_their_full_reservation(tmp_path):
    ledger = ProviderCostLedger(0.02, path=tmp_path / "costs.json", run_id="personal", pinned=False)
    ledger.before_call("image", {"model": "muse-image-1.0"})
    assert ledger.summary()["spent_usd"] == 0.01
    assert ledger.summary()["remaining_usd"] == 0.01


def test_hard_ceiling_of_two_dollars_applies_to_the_personal_cap(tmp_path):
    with pytest.raises(ProviderBudgetError):
        ProviderCostLedger(2.01, path=tmp_path / "costs.json", run_id="personal", pinned=False)


def test_cap_error_is_a_stable_user_facing_code_not_a_generic_validation_error():
    error = classify_error(ProviderSpendCapError("Provider cost guard refused a call"))
    assert (error.code, error.status, error.retryable) == ("SPEND_CAP_REACHED", 429, False)
    assert "PROVIDER_SPEND_CAP_USD" in error.message
    assert classify_error(ProviderBudgetError("Unsupported media")).code == "INVALID_ARGUMENT"


def test_configuration_enables_the_cap_and_requires_a_durable_path(monkeypatch, tmp_path):
    _env(monkeypatch, tmp_path)
    ledger = build_cost_ledger_from_env()
    assert ledger is not None and ledger.summary()["cap_usd"] == 0.02
    assert (tmp_path / "provider-costs.json").exists()
    monkeypatch.delenv("PROVIDER_COST_LEDGER_PATH")
    with pytest.raises(ValueError, match="PROVIDER_COST_LEDGER_PATH"):
        build_cost_ledger_from_env()
    monkeypatch.setenv("PROVIDER_SPEND_CAP_USD", "")
    assert build_cost_ledger_from_env() is None
    monkeypatch.delenv("PROVIDER_SPEND_CAP_USD")
    assert build_cost_ledger_from_env() is None


def test_provider_spend_endpoint_reports_total_and_remaining(monkeypatch, tmp_path):
    _env(monkeypatch, tmp_path, cap="1.5")
    adapters = build_adapters_from_env()
    _image_adapter(adapters.cost_ledger, []).generate("A lighthouse", [], "u", "storybook")
    with TestClient(create_app(adapters)) as client:
        body = client.get("/internal/v1/provider-spend").json()
    assert body == {
        "enabled": True,
        "cap_usd": 1.5,
        "spent_usd": 0.01,
        "remaining_usd": 1.49,
        "calls": 1,
        "uncertain_calls": 0,
    }


def test_provider_spend_endpoint_reports_disabled_without_a_cap(monkeypatch):
    for name in ("LANGUAGE", "IMAGE", "VISION", "QUEUE", "CLOCK", "STORAGE", "MEDIA_FETCH"):
        monkeypatch.setenv("ADAPTER_" + name, "fake")
    monkeypatch.delenv("PROVIDER_SPEND_CAP_USD", raising=False)
    monkeypatch.delenv("ICOMIC_E2E_LIVE", raising=False)
    with TestClient(create_app(build_adapters_from_env())) as client:
        assert client.get("/internal/v1/provider-spend").json() == {"enabled": False}


def test_failed_generation_logs_one_correlated_line_without_prompt_text(
    monkeypatch, tmp_path, caplog
):
    _env(monkeypatch, tmp_path, cap="0.01")
    adapters = build_adapters_from_env()
    ledger = adapters.cost_ledger
    ledger.before_call("image", {"model": "muse-image-1.0"})  # Spends the whole cap.
    payload = {
        "job_id": "00000000-0000-4000-8000-000000000042",
        "project_id": "00000000-0000-4000-8000-000000000044",
        "character_id": "00000000-0000-4000-8000-000000000043",
        "character_name": "Nia",
        "prompt": "SECRET-PROMPT-TEXT",
        "hashed_user_id": "u" * 64,
        "style": "storybook",
    }
    adapters.image = _image_adapter(ledger, [])
    with (
        TestClient(create_app(adapters)) as client,
        caplog.at_level(logging.INFO, logger="app.services.worker"),
    ):
        response = client.post("/internal/v1/generate-character-reference", json=payload)
    assert response.status_code == 429 and response.json()["code"] == "SPEND_CAP_REACHED"
    lines = [r.getMessage() for r in caplog.records if "generation failed" in r.getMessage()]
    assert len(lines) == 1
    assert "job_id=00000000-0000-4000-8000-000000000042" in lines[0]
    assert "code=SPEND_CAP_REACHED" in lines[0] and "error=ProviderSpendCapError" in lines[0]
    assert "SECRET-PROMPT-TEXT" not in caplog.text
