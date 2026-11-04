"""Verify partial and startup failures produce cost reports without live requests."""

import importlib.util
import json
from pathlib import Path

import pytest

from app.provider_costs import ProviderCostLedger

RUNNER_PATH = Path(__file__).resolve().parents[3] / "scripts/e2e/comic_e2e.py"
spec = importlib.util.spec_from_file_location("comic_runner_reports", RUNNER_PATH)
runner = importlib.util.module_from_spec(spec)
spec.loader.exec_module(runner)


@pytest.mark.parametrize("startup_failure", [False, True])
def test_fake_failure_report_is_written_without_claiming_a_pdf(
    monkeypatch, tmp_path, startup_failure
):
    def services():
        if startup_failure:
            raise runner.E2EError("Service failed")

    def flow(guard, artifact_dir):
        guard.record("language", "job-1", 0, 0)
        raise runner.E2EError("Panel failure with private prompt text")

    monkeypatch.setattr(runner, "ensure_services", services)
    monkeypatch.setattr(runner, "run_flow", flow)
    (tmp_path / "latest.pdf").write_bytes(b"old fake PDF")
    assert runner.main(["--artifact-dir", str(tmp_path)]) == 1
    report = json.loads((tmp_path / "cost-report.json").read_text())
    assert report["status"] == "failed" and report["pdf_path"] is None
    assert "private prompt" not in json.dumps(report)
    assert (tmp_path / "latest.pdf").read_bytes() == b"old fake PDF"
    assert len(list(tmp_path.glob("cost-report-*.json"))) == 1


def test_runner_reconciles_all_provider_calls_including_moderation_and_failure(tmp_path):
    path = tmp_path / "costs.json"
    ledger = ProviderCostLedger(2, prior="0.017006", reserve="0.0196245", path=path)
    first = ledger.before_call("language", {"model": "muse-spark-1.3", "max_tokens": 1056})
    ledger.finish_call(first, 200, {"usage": {"prompt_tokens": 17, "completion_tokens": 64}})
    second = ledger.before_call("image", {"model": "muse-image-1.0"})
    ledger.finish_call(second, 500)
    guard = runner.CostGuard(2, True, ledger_path=path, prior_spend=0.017006)
    guard.refresh()
    assert guard.actual == 0.00029325
    assert len(guard.stages) == 2
    assert guard.provider_report["accounted_spend_usd"] == 0.01029325
    assert guard.provider_report["unpriced_calls"] == 1


def test_runner_refuses_missing_ledger_before_any_api_request(tmp_path):
    guard = runner.CostGuard(2, True, ledger_path=tmp_path / "missing.json")
    with pytest.raises(runner.E2EError, match="unavailable"):
        guard.before_call("language")


def test_live_partial_failure_report_uses_durable_ledger_without_live_requests(
    monkeypatch, tmp_path
):
    for name in ("LANGUAGE", "VISION", "IMAGE"):
        monkeypatch.setenv("ADAPTER_" + name, "meta")
    monkeypatch.setattr(runner, "live_authorization_error", lambda *args: None)
    for name in (
        "ICOMIC_E2E_RUN_ID",
        "ICOMIC_E2E_COST_DIR",
        "ICOMIC_E2E_COST_CAP_USD",
        "ICOMIC_E2E_PRIOR_SPEND_USD",
        "ICOMIC_E2E_THIRD_CHECK_RESERVE_USD",
    ):
        monkeypatch.setenv(name, "")

    def services():
        run_id = runner.os.environ["ICOMIC_E2E_RUN_ID"]
        ledger = ProviderCostLedger(
            2,
            prior="0.017006",
            reserve="0.0196245",
            path=tmp_path / f"provider-costs-{run_id}.json",
            run_id=run_id,
        )
        index = ledger.before_call("language", {"model": "muse-spark-1.3", "max_tokens": 1056})
        ledger.finish_call(index, 200, {"usage": {"prompt_tokens": 17, "completion_tokens": 64}})
        index = ledger.before_call("image", {"model": "muse-image-1.0"})
        ledger.finish_call(index, 500)

    def fail(guard, artifact_dir):
        guard.project_id = "test-project"
        raise runner.E2EError("Provider failed")

    monkeypatch.setattr(runner, "ensure_services", services)
    monkeypatch.setattr(runner, "run_flow", fail)
    assert (
        runner.main(
            [
                "--live",
                "--cost-cap-usd",
                "2",
                "--prior-spend-usd",
                "0.017006",
                "--artifact-dir",
                str(tmp_path),
            ]
        )
        == 1
    )
    report = json.loads((tmp_path / "cost-report.json").read_text())
    assert report["project_id"] == "test-project"
    assert report["actual_spend_usd"] == 0.00029325
    assert report["cumulative_observed_spend_usd"] == 0.01729925
    assert report["provider_cost_report"]["accounted_spend_usd"] == 0.01029325
    assert report["cost_complete"] is False
    assert report["status"] == "failed" and report["pdf_path"] is None
