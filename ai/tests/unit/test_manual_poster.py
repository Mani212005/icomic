"""Hermetic budget and failure-boundary tests for the manual poster runner."""

from __future__ import annotations

import json
import sys
from pathlib import Path

import httpx
import pytest

REPO_ROOT = Path(__file__).resolve().parents[3]
sys.path.insert(0, str(REPO_ROOT / "scripts" / "manual"))

from generate_poster import HARD_CAP_USD, generate_candidates, validate_count

from app.provider_costs import ProviderBudgetError, ProviderCostLedger

FIXTURE = REPO_ROOT / "ai" / "tests" / "fixtures" / "meta" / "responses_image.json"
BASE_URL = "http://mock.meta.internal/v1"
SECRET = "poster-secret-test-key"


def test_count_is_hard_limited():
    with pytest.raises(ValueError, match="between 1 and 8"):
        validate_count(9)


def test_cost_cap_refuses_before_transport(tmp_path: Path):
    assert HARD_CAP_USD == 0.50
    ledger_path = tmp_path / "poster-spend.json"
    ledger = ProviderCostLedger(cap=HARD_CAP_USD, path=ledger_path, run_id="icomic-poster")
    payload = {"model": "muse-image-1.0", "input": "seed"}
    for _ in range(50):
        ledger.before_call("image", payload)
    with pytest.raises(ProviderBudgetError, match="refused"):
        ledger.before_call("image", payload)

    calls: list[httpx.Request] = []
    results = generate_candidates(
        count=1,
        api_key=SECRET,
        ledger_path=ledger_path,
        output_dir=tmp_path / "out",
        base_url=BASE_URL,
        transport=httpx.MockTransport(
            lambda request: calls.append(request) or httpx.Response(200, json={})
        ),
    )
    assert calls == []
    assert results[0]["status"] == "refused"


def test_key_never_appears_in_result_or_spend_file(tmp_path: Path):
    captured_headers: list[str] = []

    def handle(request: httpx.Request) -> httpx.Response:
        captured_headers.append(request.headers["authorization"])
        return httpx.Response(200, json=json.loads(FIXTURE.read_text()))

    ledger_path = tmp_path / "poster-spend.json"
    results = generate_candidates(
        count=1,
        api_key=SECRET,
        ledger_path=ledger_path,
        output_dir=tmp_path / "out",
        base_url=BASE_URL,
        transport=httpx.MockTransport(handle),
    )
    assert captured_headers == [f"Bearer {SECRET}"]
    assert SECRET not in json.dumps(results)
    assert SECRET not in ledger_path.read_text()
    assert (tmp_path / "out" / "poster-01.png").is_file()


def test_api_error_stops_without_retry_and_discards_raw_response(tmp_path: Path):
    calls: list[httpx.Request] = []
    raw_error = f"{SECRET} confidential prompt in upstream response"

    def fail(request: httpx.Request) -> httpx.Response:
        calls.append(request)
        return httpx.Response(500, text=raw_error)

    ledger_path = tmp_path / "poster-spend.json"
    results = generate_candidates(
        count=8,
        api_key=SECRET,
        ledger_path=ledger_path,
        output_dir=tmp_path / "out",
        base_url=BASE_URL,
        transport=httpx.MockTransport(fail),
    )
    serialized = json.dumps(results) + ledger_path.read_text()
    assert len(calls) == 1
    assert SECRET not in serialized
    assert "confidential prompt" not in serialized
    assert results[0]["status"] == "failed"
