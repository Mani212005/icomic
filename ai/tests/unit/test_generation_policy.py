"""Observed moderation load fits the new wire cap without accepting truncation."""

import json
from pathlib import Path
from uuid import uuid4

import httpx
import pytest
from fastapi.testclient import TestClient

from app.adapters.factory import build_adapters_from_env
from app.adapters.language.fake import FakeLanguageAdapter
from app.adapters.language.meta import MetaLanguageAdapter
from app.costs import GENERATION_POLICIES, calculate_token_cost
from app.main import create_app
from app.provider_costs import ProviderCostLedger
from app.provider_diagnostics import ProviderDiagnostics
from app.services.script import ScriptService

FIXTURE = Path(__file__).resolve().parents[1] / "fixtures/meta/observed/moderation_length_873.json"


@pytest.mark.parametrize("fake", [False, True])
@pytest.mark.parametrize("truncated", [False, True])
def test_observed_reasoning_load_and_real_truncation(tmp_path, fake, truncated):
    observed = json.loads(FIXTURE.read_text())["body"]
    assert len(observed["choices"][0]["message"]["content"]) == 797
    assert observed["usage"]["completion_tokens_details"]["reasoning_tokens"] == 873
    assert observed["choices"][0]["finish_reason"] == "length"
    calls = []
    policy = GENERATION_POLICIES["moderation"]

    def respond(request):
        if request.url.path.endswith("/responses/input_tokens"):
            return httpx.Response(200, json={"input_tokens": 100})
        payload = json.loads(request.content)
        calls.append(payload)
        assert payload["max_tokens"] == policy.answer_tokens + policy.reasoning_reserve
        assert payload["reasoning_effort"] == "minimal"
        body = json.loads(json.dumps(observed))
        if not truncated and payload["max_tokens"] > observed["usage"]["completion_tokens"]:
            # Synthetic completed decision with the exact observed reasoning/usage
            # load. This proves headroom, not a successful live completion.
            body["choices"][0]["message"]["content"] = "SAFE"
            body["choices"][0]["finish_reason"] = "stop"
        return httpx.Response(200, json=body)

    ledger = ProviderCostLedger(2, path=tmp_path / "costs.json")
    diagnostics = ProviderDiagnostics(tmp_path / "diagnostics")
    replay = MetaLanguageAdapter(
        api_key="hermetic-replay",
        base_url="http://mock.meta.internal/v1",
        transport=httpx.MockTransport(respond),
        cost_ledger=ledger,
        diagnostics=diagnostics,
    )
    adapters = build_adapters_from_env()
    adapters.language = FakeLanguageAdapter(replay_adapter=replay) if fake else replay
    adapters.cost_ledger = ledger
    adapters.diagnostics = diagnostics
    client = TestClient(create_app(adapters))
    payload = {
        "job_id": str(uuid4()),
        "project_id": str(uuid4()),
        "hashed_user_id": "synthetic",
        "prompt": "A neutral fictional scene.",
    }
    response = client.post("/internal/v1/moderate-prompt", json=payload)
    assert response.status_code == (500 if truncated else 200), response.text
    status = client.get(f"/internal/v1/job/{payload['job_id']}/status").json()
    assert status["status"] == ("failed" if truncated else "done")
    assert status["actual_cost"] == pytest.approx(calculate_token_cost(34, 1056))
    report = ledger.report()
    assert report["calls"][0]["usage"]["reasoning_tokens"] == 873
    assert report["calls"][0]["output_bound"] == policy.answer_tokens + policy.reasoning_reserve
    assert report["actual_spend_usd"] == pytest.approx(calculate_token_cost(34, 1056))
    assert report["unpriced_calls"] == 0
    assert len(list((tmp_path / "diagnostics").glob("*.json"))) == int(truncated)
    assert (
        client.post("/internal/v1/moderate-prompt", json=payload).status_code
        == response.status_code
    )
    assert len(calls) == 1


def test_main_character_bound_preserves_profiles_and_ranks_source_mentions():
    class Cast(FakeLanguageAdapter):
        def complete(self, model, messages, **kwargs):
            profile = json.loads(super().complete(model, messages, **kwargs))[0]
            return json.dumps([{**profile, "name": f"Hero{n:02}"} for n in range(12)])

    profiles, _, _, warnings = ScriptService(Cast()).analyze_script("Hero11 " * 20 + "Hero10 " * 10)
    assert len(profiles) == 12
    mains = [p.name for p in profiles if p.visual_attributes["reference_role"] == "main"]
    extras = [p.name for p in profiles if p.visual_attributes["reference_role"] == "extra"]
    assert len(mains) == 6 and len(extras) == 6
    assert "Hero11" in mains and "Hero10" in mains
    assert any("extras" in warning for warning in warnings)
