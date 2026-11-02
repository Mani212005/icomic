"""Hermetic unit tests for live check budget logic, spend ledger, and safety controls."""

from __future__ import annotations

import io
import json
import sys
from pathlib import Path
from typing import Any

import httpx
import pytest

# Add scripts directory to sys.path
REPO_ROOT = Path(__file__).resolve().parent.parent.parent.parent
SCRIPTS_PATH = REPO_ROOT / "scripts"
if str(SCRIPTS_PATH) not in sys.path:
    sys.path.insert(0, str(SCRIPTS_PATH))

from manual.meta_live_check import (
    LiveCheckRunner,
    SpendLedger,
    calculate_token_cost,
    main,
    validate_budget,
)

FIXTURES_DIR = Path(__file__).resolve().parent.parent / "fixtures" / "meta"
MOCK_BASE_URL = "http://mock.meta.internal/v1"
TEST_SECRET_KEY = "super-secret-test-key-val-789"


def load_fixture(name: str) -> dict[str, Any]:
    with open(FIXTURES_DIR / name, "r", encoding="utf-8") as f:
        return json.load(f)


def create_success_transport() -> httpx.MockTransport:
    lang_fixture = load_fixture("chat_completion.json")
    vis_fixture = load_fixture("vision_review.json")
    img_fixture = load_fixture("responses_image.json")

    def handler(request: httpx.Request) -> httpx.Response:
        url_str = str(request.url)
        if "/chat/completions" in url_str:
            body = json.loads(request.content.decode("utf-8"))
            content = body["messages"][0]["content"]
            # Vision uses content parts
            if isinstance(content, list):
                return httpx.Response(
                    200, json=vis_fixture, headers={"x-request-id": "req-mock-vis"}
                )
            return httpx.Response(200, json=lang_fixture, headers={"x-request-id": "req-mock-lang"})
        if "/responses" in url_str:
            return httpx.Response(200, json=img_fixture, headers={"x-request-id": "req-mock-img"})
        return httpx.Response(404, json={"error": "not found"})

    return httpx.MockTransport(handler)


def test_calculate_token_cost():
    # 1,000,000 input tokens = $1.25, 1,000,000 output tokens = $4.25
    cost = calculate_token_cost(1_000_000, 1_000_000)
    assert round(cost, 2) == 5.50

    cost_small = calculate_token_cost(100, 20)
    assert cost_small > 0
    assert cost_small < 0.001


def test_spend_ledger_tracks_records_and_cumulative_spend(tmp_path: Path):
    ledger_file = tmp_path / "test-spend.jsonl"
    ledger = SpendLedger(ledger_path=ledger_file)

    # Initial spend is 0.0
    initial_spend, records = ledger.read_cumulative_spend()
    assert initial_spend == 0.0
    assert len(records) == 0

    # Record 1
    ledger.record_call(
        model="muse-spark-1.3",
        adapter="language",
        cost_usd=0.0001,
        input_tokens=15,
        output_tokens=5,
    )
    # Record 2
    ledger.record_call(
        model="muse-image-1.0",
        adapter="image",
        cost_usd=0.01,
        image_count=1,
    )

    # Verify cumulative total
    cumulative, records = ledger.read_cumulative_spend()
    assert len(records) == 2
    assert round(cumulative, 4) == 0.0101
    assert records[0]["adapter"] == "language"
    assert records[1]["adapter"] == "image"


def test_spend_ledger_unwritable_path_raises():
    bad_path = Path("/proc/invalid_path/.live-spend.jsonl")
    ledger = SpendLedger(ledger_path=bad_path)
    with pytest.raises(PermissionError):
        ledger.verify_accessible()


def test_budget_aborts_before_call_when_ceiling_exceeded(tmp_path: Path):
    ledger_file = tmp_path / "spend_abort.jsonl"
    ledger = SpendLedger(ledger_path=ledger_file)

    # Set budget to $0.005, which is enough for language and vision (~$0.0006)
    # but NOT enough for image generation ($0.01)
    runner = LiveCheckRunner(
        api_key=TEST_SECRET_KEY,
        base_url=MOCK_BASE_URL,
        budget_usd=0.005,
        ledger=ledger,
        transport=create_success_transport(),
    )

    success, summary = runner.run()
    assert success is False
    assert "stage=image" in summary
    assert "budget_exceeded" in summary

    # Verify that image call was aborted BEFORE execution
    _, records = ledger.read_cumulative_spend()
    assert len(records) == 2  # language and vision succeeded, image was aborted
    assert records[0]["adapter"] == "language"
    assert records[1]["adapter"] == "vision"


def test_cumulative_spend_across_runs_triggers_budget_abort(tmp_path: Path):
    ledger_file = tmp_path / "spend_multi_run.jsonl"
    ledger = SpendLedger(ledger_path=ledger_file)

    # Simulate past runs having spent $4.995 of $5.00 budget
    ledger.record_call(
        model="muse-image-1.0",
        adapter="image",
        cost_usd=4.995,
        image_count=500,
    )

    # Now run with $5.00 budget: even whole checklist worst case (~0.0107) exceeds 5.00
    runner = LiveCheckRunner(
        api_key=TEST_SECRET_KEY,
        base_url=MOCK_BASE_URL,
        budget_usd=5.00,
        ledger=ledger,
        transport=create_success_transport(),
    )

    success, summary = runner.run()
    # Language (~0.0001) might fit or image will abort
    assert success is False
    assert "budget_exceeded" in summary


def test_stop_at_first_failure(tmp_path: Path):
    ledger_file = tmp_path / "spend_fail.jsonl"
    ledger = SpendLedger(ledger_path=ledger_file)
    attempts: list[str] = []

    def failing_handler(request: httpx.Request) -> httpx.Response:
        attempts.append(str(request.url))
        # Fail immediately on first call (language)
        return httpx.Response(500, text="Internal Server Error")

    runner = LiveCheckRunner(
        api_key=TEST_SECRET_KEY,
        base_url=MOCK_BASE_URL,
        budget_usd=5.00,
        ledger=ledger,
        transport=httpx.MockTransport(failing_handler),
    )

    success, summary = runner.run()
    assert success is False
    assert "stage=language" in summary
    assert len(attempts) == 1  # Exactly one call attempted; vision & image never called
    assert "calls=0/3" in summary


def test_key_never_printed_or_logged(tmp_path: Path, monkeypatch):
    ledger_file = tmp_path / "spend_privacy.jsonl"
    ledger = SpendLedger(ledger_path=ledger_file)

    runner = LiveCheckRunner(
        api_key=TEST_SECRET_KEY,
        base_url=MOCK_BASE_URL,
        budget_usd=5.00,
        ledger=ledger,
        transport=create_success_transport(),
    )

    # Capture stdout and stderr
    stdout_capture = io.StringIO()
    stderr_capture = io.StringIO()
    monkeypatch.setattr(sys, "stdout", stdout_capture)
    monkeypatch.setattr(sys, "stderr", stderr_capture)

    success, summary = runner.run()
    assert success is True

    out = stdout_capture.getvalue()
    err = stderr_capture.getvalue()

    assert TEST_SECRET_KEY not in out, "API key was leaked to stdout"
    assert TEST_SECRET_KEY not in err, "API key was leaked to stderr"
    assert TEST_SECRET_KEY not in summary, "API key was leaked to summary string"


def test_successful_run_order_and_summary_line(tmp_path: Path):
    ledger_file = tmp_path / "spend_success.jsonl"
    ledger = SpendLedger(ledger_path=ledger_file)
    call_order: list[str] = []

    def tracking_handler(request: httpx.Request) -> httpx.Response:
        url_str = str(request.url)
        if "/chat/completions" in url_str:
            body = json.loads(request.content.decode("utf-8"))
            if isinstance(body["messages"][0]["content"], list):
                call_order.append("vision")
                return httpx.Response(200, json=load_fixture("vision_review.json"))
            call_order.append("language")
            return httpx.Response(200, json=load_fixture("chat_completion.json"))
        if "/responses" in url_str:
            call_order.append("image")
            return httpx.Response(200, json=load_fixture("responses_image.json"))
        return httpx.Response(404)

    runner = LiveCheckRunner(
        api_key=TEST_SECRET_KEY,
        base_url=MOCK_BASE_URL,
        budget_usd=5.00,
        ledger=ledger,
        transport=httpx.MockTransport(tracking_handler),
    )

    success, summary = runner.run()
    assert success is True
    # Verify execution order: text (language) -> vision -> image
    assert call_order == ["language", "vision", "image"]

    # Verify summary line format
    assert summary.startswith("STATUS: PASS ")
    assert "calls=3/3" in summary
    assert "budget_usd=5.00" in summary


def test_safety_gate_main_refuses_without_live_check_env(monkeypatch):
    """main() must immediately refuse without META_LIVE_CHECK=1 and build zero clients."""
    monkeypatch.delenv("META_LIVE_CHECK", raising=False)
    stderr_capture = io.StringIO()
    monkeypatch.setattr(sys, "stderr", stderr_capture)

    exit_code = main()
    assert exit_code == 1
    err = stderr_capture.getvalue()
    assert "Safety gate active" in err
    assert "META_LIVE_CHECK is not set to '1'" in err


def test_budget_validation_refuses_above_5_without_permission(monkeypatch):
    """Budget above $5.00 must refuse unless owner explicitly allows it."""
    monkeypatch.delenv("META_LIVE_BUDGET_ALLOW_ABOVE_DEFAULT", raising=False)

    with pytest.raises(PermissionError) as exc_info:
        validate_budget("5.01")
    assert "exceeds default $5.00 limit" in str(exc_info.value)
    assert "owner's word" in str(exc_info.value)

    # With permission flag set, it should succeed
    monkeypatch.setenv("META_LIVE_BUDGET_ALLOW_ABOVE_DEFAULT", "1")
    assert validate_budget("10.00") == 10.00


def test_budget_validation_refuses_invalid_or_non_positive():
    """Budget must be a positive number."""
    with pytest.raises(ValueError) as exc_info:
        validate_budget("not-a-number")
    assert "must be a positive number" in str(exc_info.value)

    with pytest.raises(ValueError) as exc_info:
        validate_budget("-1.5")
    assert "must be positive" in str(exc_info.value)

    with pytest.raises(ValueError) as exc_info:
        validate_budget("0")
    assert "must be positive" in str(exc_info.value)

    # Empty or None uses default
    assert validate_budget(None) == 5.00
    assert validate_budget("") == 5.00


def test_failed_call_records_worst_case_cost_in_ledger_with_failed_flag(tmp_path: Path):
    """When a call fails after request transmission, record worst-case cost flagged failed=True."""
    ledger_file = tmp_path / "spend_failed_call.jsonl"
    ledger = SpendLedger(ledger_path=ledger_file)

    def failing_handler(request: httpx.Request) -> httpx.Response:
        return httpx.Response(500, text="Internal Server Error")

    runner = LiveCheckRunner(
        api_key=TEST_SECRET_KEY,
        base_url=MOCK_BASE_URL,
        budget_usd=5.00,
        ledger=ledger,
        transport=httpx.MockTransport(failing_handler),
    )

    success, summary = runner.run()
    assert success is False
    assert "stage=language" in summary

    # Verify that ledger has recorded the failed call with worst-case cost and failed=True
    cumulative, records = ledger.read_cumulative_spend()
    assert len(records) == 1
    record = records[0]
    assert record["adapter"] == "language"
    assert record["failed"] is True
    assert record["estimated_cost_usd"] > 0
    assert cumulative == record["estimated_cost_usd"]
    assert "spend_usd=" in summary
    assert "0.000000" not in summary.split("spend_usd=")[1][:8]  # Recorded positive spend


def test_key_found_printed_without_length(tmp_path: Path, monkeypatch):
    """Script prints [FOUND] without printing key length."""
    ledger_file = tmp_path / "spend_key_display.jsonl"
    ledger = SpendLedger(ledger_path=ledger_file)

    runner = LiveCheckRunner(
        api_key=TEST_SECRET_KEY,
        base_url=MOCK_BASE_URL,
        budget_usd=5.00,
        ledger=ledger,
        transport=create_success_transport(),
    )

    stdout_capture = io.StringIO()
    monkeypatch.setattr(sys, "stdout", stdout_capture)

    success, _ = runner.run()
    assert success is True

    out = stdout_capture.getvalue()
    assert "[FOUND]" in out
    assert "length:" not in out
    assert str(len(TEST_SECRET_KEY)) not in out
