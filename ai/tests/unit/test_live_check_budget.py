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
    calculate_checklist_worst_case,
    calculate_token_cost,
    estimate_call_worst_case,
    main,
    validate_budget,
)

from app.costs import GENERATION_POLICIES

FIXTURES_DIR = Path(__file__).resolve().parent.parent / "fixtures" / "meta"
MOCK_BASE_URL = "http://mock.meta.internal/v1"
TEST_SECRET_KEY = "super-secret-test-key-val-789"


@pytest.fixture(autouse=True)
def reasoning_defaults(monkeypatch):
    monkeypatch.delenv("META_REASONING_EFFORT", raising=False)
    monkeypatch.delenv("META_REASONING_RESERVE_TOKENS", raising=False)
    monkeypatch.delenv("META_ALLOW_CONTRIBUTOR_TIER", raising=False)


def request_stage(request):
    if request.url.path.endswith("/responses"):
        return "image"
    body = json.loads(request.content)
    return "vision" if isinstance(body["messages"][0]["content"], list) else "language"


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

    # Both chat caps include reasoning headroom; $0.01 still refuses the image stage.
    runner = LiveCheckRunner(
        api_key=TEST_SECRET_KEY,
        base_url=MOCK_BASE_URL,
        budget_usd=0.01,
        ledger=ledger,
        diagnostics_path=tmp_path / "diagnostics_abort.json",
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
        diagnostics_path=tmp_path / "diagnostics_multi.json",
        transport=create_success_transport(),
    )

    success, summary = runner.run()
    # Language (~0.0001) might fit or image will abort
    assert success is False
    assert "budget_exceeded" in summary


@pytest.mark.parametrize("failed_stage", ["language", "vision", "image", "all"])
def test_stages_continue_independently_after_failure(tmp_path: Path, failed_stage):
    ledger = SpendLedger(tmp_path / "spend.jsonl")
    attempts = []
    diag_file = tmp_path / "diagnostics.json"
    success_transport = create_success_transport()

    def handler(request):
        stage = request_stage(request)
        attempts.append(stage)
        if failed_stage in (stage, "all"):
            return httpx.Response(500, text="Internal Server Error")
        return success_transport.handle_request(request)

    runner = LiveCheckRunner(
        api_key=TEST_SECRET_KEY,
        base_url=MOCK_BASE_URL,
        ledger=ledger,
        diagnostics_path=diag_file,
        transport=httpx.MockTransport(handler),
    )
    success, summary = runner.run()
    assert success is False
    assert summary.startswith("STATUS: FAIL " if failed_stage == "all" else "STATUS: PARTIAL ")
    assert attempts == ["language", "vision", "image"]
    assert "calls=3/3" in summary
    stages = json.loads(diag_file.read_text())["stages"]
    _, records = ledger.read_cumulative_spend()
    assert len(records) == 3
    for stage, record in zip(attempts, records):
        failed = failed_stage in (stage, "all")
        assert stages[stage]["stage_status"] == ("FAILED" if failed else "SUCCESS")
        assert record["failed"] is failed
        assert f"{stage}={'FAILED' if failed else 'SUCCESS'}" in summary
        if failed:
            assert record["estimated_cost_usd"] == pytest.approx(estimate_call_worst_case(stage))


def test_key_never_printed_or_logged(tmp_path: Path, monkeypatch):
    ledger_file = tmp_path / "spend_privacy.jsonl"
    ledger = SpendLedger(ledger_path=ledger_file)

    runner = LiveCheckRunner(
        api_key=TEST_SECRET_KEY,
        base_url=MOCK_BASE_URL,
        budget_usd=5.00,
        ledger=ledger,
        diagnostics_path=tmp_path / "diagnostics_privacy.json",
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
        diagnostics_path=tmp_path / "diagnostics_success.json",
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
    import manual.meta_live_check as live_check

    def forbidden(*args, **kwargs):
        pytest.fail("main safety gate accessed credentials or constructed a client")

    monkeypatch.setattr(live_check, "resolve_api_key", forbidden)
    monkeypatch.setattr(live_check, "MetaHttpClient", forbidden)
    monkeypatch.delenv("META_LIVE_CHECK", raising=False)
    stderr_capture = io.StringIO()
    monkeypatch.setattr(sys, "stderr", stderr_capture)

    exit_code = main()
    assert exit_code == 1
    err = stderr_capture.getvalue()
    assert "Safety gate active" in err
    assert "META_LIVE_CHECK is not set to '1'" in err


def test_budget_validation_refuses_above_5_even_with_legacy_override(monkeypatch):
    """The hard cap applies even if the old override flag remains in the environment."""
    monkeypatch.delenv("META_LIVE_BUDGET_ALLOW_ABOVE_DEFAULT", raising=False)
    with pytest.raises(PermissionError, match=r"hard \$5.00 limit"):
        validate_budget("5.01")
    monkeypatch.setenv("META_LIVE_BUDGET_ALLOW_ABOVE_DEFAULT", "1")
    with pytest.raises(PermissionError, match=r"hard \$5.00 limit"):
        validate_budget("10.00")


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
        diagnostics_path=tmp_path / "diagnostics_failed_call.json",
        transport=httpx.MockTransport(failing_handler),
    )

    success, summary = runner.run()
    assert success is False
    assert summary.startswith("STATUS: FAIL ")

    # Verify that ledger has recorded the failed call with worst-case cost and failed=True
    cumulative, records = ledger.read_cumulative_spend()
    assert len(records) == 3
    record = records[0]
    assert record["adapter"] == "language"
    assert record["failed"] is True
    assert record["estimated_cost_usd"] > 0
    assert cumulative == pytest.approx(calculate_checklist_worst_case())
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
        diagnostics_path=tmp_path / "diagnostics_key_display.json",
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


def test_diagnostics_file_sanitization_and_keys(tmp_path: Path):
    """Diagnostics file must be created on run, contain all required sanitized keys, and never leak keys or prompts."""
    ledger_file = tmp_path / "spend_diag.jsonl"
    ledger = SpendLedger(ledger_path=ledger_file)
    diag_file = tmp_path / ".live-diagnostics.json"

    runner = LiveCheckRunner(
        api_key=TEST_SECRET_KEY,
        base_url=MOCK_BASE_URL,
        budget_usd=5.00,
        ledger=ledger,
        diagnostics_path=diag_file,
        transport=create_success_transport(),
    )

    success, _ = runner.run()
    assert success is True
    assert diag_file.is_file(), "Diagnostics file was not created"

    raw_text = diag_file.read_text(encoding="utf-8")
    assert TEST_SECRET_KEY not in raw_text, "Secret key leaked in diagnostics file"
    assert "Authorization" not in raw_text, "Headers leaked in diagnostics file"
    assert "Say 'hello' in one word" not in raw_text, "Prompt leaked in diagnostics file"
    assert "mock assistant response" not in raw_text, "Response body leaked in diagnostics file"

    data = json.loads(raw_text)
    assert "timestamp" in data
    assert "stages" in data

    stages = data["stages"]
    assert "language" in stages
    assert "vision" in stages
    assert "image" in stages

    lang_stage = stages["language"]
    assert lang_stage["status_code"] == 200
    assert isinstance(lang_stage["top_level_keys"], list)
    assert "choices" in lang_stage["top_level_keys"]
    assert lang_stage["finish_reason"] == "stop"
    assert lang_stage["usage"]["input_tokens"] == 16
    assert lang_stage["usage"]["output_tokens"] == 14
    assert lang_stage["content_length"] > 0

    vis_stage = stages["vision"]
    assert vis_stage["status_code"] == 200
    assert isinstance(vis_stage["top_level_keys"], list)
    assert vis_stage["finish_reason"] == "stop"
    assert vis_stage["usage"]["input_tokens"] == 42
    assert vis_stage["usage"]["output_tokens"] == 16
    assert vis_stage["content_length"] > 0

    img_stage = stages["image"]
    assert img_stage["status_code"] == 200
    assert isinstance(img_stage["top_level_keys"], list)
    assert img_stage["detected_media_type"] == "image/png"
    assert img_stage["first_12_bytes_hex"] is not None
    assert isinstance(img_stage["first_12_bytes_hex"], str)
    assert len(img_stage["first_12_bytes_hex"]) == 24  # 12 bytes = 24 hex chars
    assert img_stage["image_dimensions"] == {"width": 1, "height": 1}
    assert isinstance(img_stage["provenance_keys"], list)
    assert img_stage["content_length"] > 0


@pytest.mark.parametrize("status_code", [401, 403])
@pytest.mark.parametrize("auth_stage", ["language", "vision", "image"])
def test_authentication_error_stops_all_remaining_stages(tmp_path, status_code, auth_stage):
    attempts = []
    success_transport = create_success_transport()
    diag_file = tmp_path / "diagnostics.json"
    ledger = SpendLedger(tmp_path / "spend.jsonl")

    def handler(request):
        stage = request_stage(request)
        attempts.append(stage)
        if stage == auth_stage:
            return httpx.Response(status_code, text="denied")
        return success_transport.handle_request(request)

    runner = LiveCheckRunner(
        api_key=TEST_SECRET_KEY,
        base_url=MOCK_BASE_URL,
        ledger=ledger,
        diagnostics_path=diag_file,
        transport=httpx.MockTransport(handler),
    )
    success, summary = runner.run()
    expected = ["language", "vision", "image"]
    expected = expected[: expected.index(auth_stage) + 1]
    assert success is False
    assert attempts == expected
    assert f"stage={auth_stage} reason=configuration_error" in summary
    assert f"calls={len(expected)}/3" in summary
    stages = json.loads(diag_file.read_text())["stages"]
    assert stages[auth_stage]["status_code"] == status_code
    assert stages[auth_stage]["stage_status"] == "FAILED"
    _, records = ledger.read_cumulative_spend()
    assert len(records) == len(expected)
    assert records[-1]["failed"] is True
    assert records[-1]["estimated_cost_usd"] == pytest.approx(estimate_call_worst_case(auth_stage))
    for stage in set(stages) - set(expected):
        assert stages[stage]["stage_status"] == "SKIPPED"
        assert f"{stage}=SKIPPED" in summary


def test_empty_reasoning_response_continues_and_preserves_diagnostics(tmp_path):
    attempts = []
    success_transport = create_success_transport()
    diag_file = tmp_path / "diagnostics.json"

    def handler(request):
        stage = request_stage(request)
        attempts.append(stage)
        if stage == "language":
            return httpx.Response(200, json=load_fixture("chat_completion_reasoning_length.json"))
        return success_transport.handle_request(request)

    runner = LiveCheckRunner(
        api_key=TEST_SECRET_KEY,
        base_url=MOCK_BASE_URL,
        ledger=SpendLedger(tmp_path / "spend.jsonl"),
        diagnostics_path=diag_file,
        transport=httpx.MockTransport(handler),
    )
    success, summary = runner.run()
    assert success is False
    assert attempts == ["language", "vision", "image"]
    assert summary.startswith("STATUS: PARTIAL ")
    language = json.loads(diag_file.read_text())["stages"]["language"]
    assert language["status_code"] == 200
    assert language["finish_reason"] == "length"
    assert language["usage"] == {
        "prompt_tokens": 17,
        "completion_tokens": 64,
        "total_tokens": 81,
        "reasoning_tokens": 62,
    }


@pytest.mark.parametrize("reserve", [0, 1024, 2048])
def test_wire_caps_and_budget_estimates_include_reserve(tmp_path, monkeypatch, reserve):
    monkeypatch.setenv("META_REASONING_RESERVE_TOKENS", str(reserve))
    captured = {}
    success_transport = create_success_transport()

    def handler(request):
        captured[request_stage(request)] = json.loads(request.content)
        return success_transport.handle_request(request)

    runner = LiveCheckRunner(
        api_key=TEST_SECRET_KEY,
        base_url=MOCK_BASE_URL,
        ledger=SpendLedger(tmp_path / "spend.jsonl"),
        diagnostics_path=tmp_path / "diag.json",
        transport=httpx.MockTransport(handler),
    )
    success, _ = runner.run()
    assert success is True
    assert captured["language"]["messages"] == [{"role": "user", "content": "Say hi."}]
    assert captured["language"]["max_tokens"] == GENERATION_POLICIES[
        "check_language"
    ].answer_tokens + max(GENERATION_POLICIES["check_language"].reasoning_reserve, reserve)
    assert captured["vision"]["max_tokens"] == GENERATION_POLICIES["review"].answer_tokens + max(
        GENERATION_POLICIES["review"].reasoning_reserve, reserve
    )
    assert captured["language"]["reasoning_effort"] == "minimal"
    assert captured["vision"]["reasoning_effort"] == "minimal"
    assert estimate_call_worst_case("language", reserve) == calculate_token_cost(
        60,
        GENERATION_POLICIES["check_language"].answer_tokens
        + max(GENERATION_POLICIES["check_language"].reasoning_reserve, reserve),
    )
    assert estimate_call_worst_case("vision", reserve) == calculate_token_cost(
        350,
        GENERATION_POLICIES["review"].answer_tokens
        + max(GENERATION_POLICIES["review"].reasoning_reserve, reserve),
    )
    assert calculate_checklist_worst_case(reserve) < 0.05


def test_reasoning_tokens_billed_as_output_without_double_counting(tmp_path):
    fixture = load_fixture("chat_completion_reasoning_length.json")
    fixture["choices"][0] = {"message": {"content": "Hi"}, "finish_reason": "stop"}
    success_transport = create_success_transport()
    ledger = SpendLedger(tmp_path / "spend.jsonl")

    def handler(request):
        if request_stage(request) == "language":
            return httpx.Response(200, json=fixture)
        return success_transport.handle_request(request)

    runner = LiveCheckRunner(
        api_key=TEST_SECRET_KEY,
        base_url=MOCK_BASE_URL,
        ledger=ledger,
        diagnostics_path=tmp_path / "diag.json",
        transport=httpx.MockTransport(handler),
    )
    assert runner.run()[0] is True
    _, records = ledger.read_cumulative_spend()
    assert records[0]["output_tokens"] == 64  # Includes the 62 hidden reasoning tokens.
    assert records[0]["estimated_cost_usd"] == pytest.approx(calculate_token_cost(17, 64))
    assert records[0]["estimated_cost_usd"] != pytest.approx(calculate_token_cost(17, 64 + 62))


def test_failed_call_reserve_is_charged_before_next_budget_check(tmp_path):
    attempts = []

    def handler(request):
        attempts.append(request_stage(request))
        return httpx.Response(500)

    runner = LiveCheckRunner(
        api_key=TEST_SECRET_KEY,
        base_url=MOCK_BASE_URL,
        budget_usd=estimate_call_worst_case("language") + estimate_call_worst_case("vision") / 2,
        ledger=SpendLedger(tmp_path / "spend.jsonl"),
        diagnostics_path=tmp_path / "diag.json",
        transport=httpx.MockTransport(handler),
    )
    success, summary = runner.run()
    assert success is False
    assert attempts == ["language"]
    assert "stage=vision reason=budget_exceeded" in summary
    stages = json.loads((tmp_path / "diag.json").read_text())["stages"]
    assert stages["language"]["stage_status"] == "FAILED"
    assert stages["vision"]["stage_status"] == "ABORTED"
    assert stages["image"]["stage_status"] == "SKIPPED"


@pytest.mark.parametrize(
    "variable,value",
    [
        ("META_REASONING_EFFORT", "none"),
        ("META_REASONING_RESERVE_TOKENS", "-1"),
        ("META_REASONING_RESERVE_TOKENS", "100000"),
        ("META_LANGUAGE_MODEL", "muse-spark-1.3-contributor"),
        ("META_VISION_MODEL", "muse-spark-1.3-contributor"),
        ("META_IMAGE_MODEL", "muse-image-1.0-contributor"),
    ],
)
def test_configuration_failure_stops_before_any_call(tmp_path, monkeypatch, variable, value):
    monkeypatch.setenv(variable, value)

    def forbidden(request):
        pytest.fail("configuration error reached HTTP transport")

    runner = LiveCheckRunner(
        api_key=TEST_SECRET_KEY,
        base_url=MOCK_BASE_URL,
        ledger=SpendLedger(tmp_path / "spend.jsonl"),
        diagnostics_path=tmp_path / "diag.json",
        transport=httpx.MockTransport(forbidden),
    )
    success, summary = runner.run()
    assert success is False
    assert "stage=configuration" in summary
    assert "calls=0/3" in summary
    stages = json.loads((tmp_path / "diag.json").read_text())["stages"]
    assert all(stage["stage_status"] == "SKIPPED" for stage in stages.values())


@pytest.mark.parametrize("budget", ["NaN", "inf", "-inf"])
def test_nonfinite_budget_refused(budget):
    with pytest.raises(ValueError, match="finite"):
        validate_budget(budget)


def test_failure_diagnostics_and_console_exclude_echoed_response(tmp_path, capsys):
    echoed = f"private prompt or response {TEST_SECRET_KEY}"
    runner = LiveCheckRunner(
        api_key=TEST_SECRET_KEY,
        base_url=MOCK_BASE_URL,
        ledger=SpendLedger(tmp_path / "spend.jsonl"),
        diagnostics_path=tmp_path / "diag.json",
        transport=httpx.MockTransport(
            lambda request: httpx.Response(
                400,
                json={"error": {"message": echoed}},
            )
        ),
    )
    assert runner.run()[0] is False
    captured = capsys.readouterr()
    diagnostics = (tmp_path / "diag.json").read_text()
    assert TEST_SECRET_KEY not in captured.out + captured.err + diagnostics
    assert "private prompt or response" not in captured.out + captured.err + diagnostics


@pytest.mark.parametrize(
    "content",
    [
        "not-json",
        '{"estimated_cost_usd": -1}',
        '{"estimated_cost_usd": "NaN"}',
        '{"estimated_cost_usd": "inf"}',
        "{}",
    ],
)
def test_invalid_ledger_aborts_without_http(tmp_path, content):
    ledger_path = tmp_path / "spend.jsonl"
    ledger_path.write_text(content + "\n")

    def forbidden(request):
        pytest.fail("invalid spend ledger reached HTTP transport")

    runner = LiveCheckRunner(
        api_key=TEST_SECRET_KEY,
        base_url=MOCK_BASE_URL,
        ledger=SpendLedger(ledger_path),
        diagnostics_path=tmp_path / "diag.json",
        transport=httpx.MockTransport(forbidden),
    )
    success, summary = runner.run()
    assert success is False
    assert "stage=ledger reason=ledger_unavailable" in summary
    assert "calls=0/3" in summary
    assert (tmp_path / "diag.json").is_file()


def test_runner_safety_gate_without_injected_transport(tmp_path, monkeypatch):
    import manual.meta_live_check as live_check

    def forbidden(*args, **kwargs):
        pytest.fail("runner safety gate constructed a client")

    monkeypatch.delenv("META_LIVE_CHECK", raising=False)
    monkeypatch.setattr(live_check, "MetaHttpClient", forbidden)
    runner = LiveCheckRunner(
        api_key=TEST_SECRET_KEY,
        base_url=MOCK_BASE_URL,
        ledger=SpendLedger(tmp_path / "spend.jsonl"),
        diagnostics_path=tmp_path / "diag.json",
    )
    success, summary = runner.run()
    assert success is False
    assert "reason=safety_gate_active" in summary
    assert "calls=0/3" in summary
