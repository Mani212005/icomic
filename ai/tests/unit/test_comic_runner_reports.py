"""Verify partial and startup failures produce cost reports without live requests."""

import importlib.util
import json
from pathlib import Path
from uuid import UUID

import pytest

from app.costs import calculate_checklist_worst_case
from app.provider_costs import ProviderCostLedger
from app.provider_diagnostics import ProviderDiagnostics

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
    ledger = ProviderCostLedger(
        2, prior="0.017006", reserve=round(calculate_checklist_worst_case(), 9), path=path
    )
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
        "ICOMIC_E2E_DIAGNOSTICS_DIR",
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
            reserve=round(calculate_checklist_worst_case(), 9),
            path=tmp_path / f"provider-costs-{run_id}.json",
            run_id=run_id,
        )
        index = ledger.before_call("language", {"model": "muse-spark-1.3", "max_tokens": 1056})
        ledger.finish_call(index, 200, {"usage": {"prompt_tokens": 17, "completion_tokens": 64}})
        index = ledger.before_call("image", {"model": "muse-image-1.0"})
        ledger.finish_call(index, 500)
        diagnostics = ProviderDiagnostics(tmp_path / f"diagnostics-{run_id}")
        diagnostics.capture(b'{"error":"full synthetic failed body"}', 500)
        diagnostics.failure("HTTPError")

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
                "--prior-spend-source",
                "recorded-previous-run.md",
                "--alert-usd",
                "0.0172",
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
    assert report["prior_spend_source"] == "recorded-previous-run.md"
    assert report["alert_threshold_usd"] == 0.0172
    assert report["alert_raised"] is True
    assert report["provider_cost_report"]["accounted_spend_usd"] == 0.01029325
    assert report["cost_complete"] is False
    assert report["status"] == "failed" and report["pdf_path"] is None
    assert len(report["diagnostic_files"]) == 1
    assert Path(report["diagnostic_files"][0]).parent == Path(report["diagnostics_dir"])


def test_artifacts_inside_repository_are_ignored(monkeypatch, tmp_path):
    monkeypatch.setattr(runner, "REPO_ROOT", tmp_path)
    monkeypatch.setattr(runner, "ensure_services", lambda: None)
    monkeypatch.setattr(runner, "run_flow", lambda *args: (0, "synthetic-project"))
    output = tmp_path / "local-proof"
    output.mkdir()
    (output / ".gitignore").write_text("existing-rule\n")
    assert runner.main(["--artifact-dir", str(output)]) == 0
    assert (output / ".gitignore").read_text() == "existing-rule\n\n*\n"


def test_runner_refuses_artifacts_at_repository_root(monkeypatch, tmp_path):
    monkeypatch.setattr(runner, "REPO_ROOT", tmp_path)
    assert runner.main(["--artifact-dir", str(tmp_path)]) == 2
    assert not (tmp_path / ".gitignore").exists()


def test_consecutive_flows_provision_distinct_users_and_use_their_headers(monkeypatch, tmp_path):
    commands = []
    authenticated_users = []

    def provision(command, **kwargs):
        commands.append((command, kwargs))
        return runner.subprocess.CompletedProcess(command, 0)

    class UsageResponse:
        def __init__(self):
            self.headers = {"Content-Type": "application/json"}

        def __enter__(self):
            return self

        def __exit__(self, *args):
            pass

        def read(self):
            return b'{"balance":100}'

    def respond(request, **kwargs):
        authenticated_users.append(request.get_header("X-user-id"))
        if request.full_url.endswith("/credit-usage"):
            return UsageResponse()
        raise runner.E2EError("stop after isolated fixture")

    monkeypatch.setattr(runner.subprocess, "run", provision)
    monkeypatch.setattr(runner, "urlopen", respond)
    users = []
    emails = []
    credit_ids = []
    for _ in range(2):
        guard = runner.CostGuard(float("inf"), False)
        with pytest.raises(runner.E2EError, match="stop after isolated fixture"):
            runner.run_flow(guard, tmp_path)
        users.append(guard.user_id)
        command, options = commands[-1]
        assert "--single-transaction" in command
        assert "--file=-" in command
        assert options["check"] is True
        assert options["capture_output"] is True
        assert "INSERT INTO users" in options["input"]
        assert "'free'" in options["input"] and "100, 100" in options["input"]
        assert "UPDATE " not in options["input"] and "DELETE " not in options["input"]
        assert "quota" not in options["input"]
        variables = dict(item.split("=", 1) for item in command if item.startswith("e2e_"))
        assert variables["e2e_user_id"] == guard.user_id
        assert UUID(guard.user_id).version == 4
        emails.append(variables["e2e_email"])
        credit_ids.append(variables["e2e_credit_id"])
    assert len(set(users)) == len(set(emails)) == len(set(credit_ids)) == 2
    assert authenticated_users == [users[0], users[0], users[1], users[1]]


def test_alert_is_logged_once_and_recorded_on_failure(monkeypatch, tmp_path, capsys):
    def fail(guard, artifact_dir):
        guard.record("language", "job-1", 0.6, 0.6)
        assert not guard.alert_raised
        guard.record("image", "job-2", 0.5, 0.5)
        guard.check_alert()
        raise runner.E2EError("Stopped after an unrelated failure")

    monkeypatch.setattr(runner, "ensure_services", lambda: None)
    monkeypatch.setattr(runner, "run_flow", fail)
    assert runner.main(["--artifact-dir", str(tmp_path), "--alert-usd", "1"]) == 1
    report = json.loads((tmp_path / "cost-report.json").read_text())
    assert report["alert_threshold_usd"] == 1
    assert report["alert_raised"] is True
    assert report["cumulative_observed_spend_usd"] == 1.1
    assert capsys.readouterr().out.count("COST ALERT:") == 1


@pytest.mark.parametrize("cap", [0.1, 1, 2])
def test_live_cap_can_be_lowered_but_never_exceeds_shared_authorization(cap):
    assert runner.live_authorization_error(True, {"ICOMIC_E2E_LIVE": "1"}, cap) is None


@pytest.mark.parametrize("cap", [None, 0, -1, 2.01, float("inf"), float("nan")])
def test_live_cap_rejects_unbounded_or_excess_authorization(cap):
    assert runner.live_authorization_error(True, {"ICOMIC_E2E_LIVE": "1"}, cap)
