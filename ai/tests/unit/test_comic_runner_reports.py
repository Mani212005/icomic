"""Verify partial and startup failures produce cost reports without live requests."""

import importlib.util
import json
import sys
import types
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


def stub_managed_stack(monkeypatch):
    monkeypatch.setattr(runner, "warn_stale_e2e_projects", lambda project: [])
    monkeypatch.setattr(runner, "teardown_managed_stack", lambda project: None)


@pytest.mark.parametrize("startup_failure", [False, True])
def test_fake_failure_report_is_written_without_claiming_a_pdf(
    monkeypatch, tmp_path, startup_failure
):
    stub_managed_stack(monkeypatch)

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


def test_new_project_title_uses_explicit_value_or_script_filename(tmp_path):
    script = tmp_path / "the_tide-clock.txt"
    script.write_text("THE TIDE CLOCK\nThe opening scene begins here.")
    assert runner.resolve_project_title(script) == "The Tide Clock"
    assert runner.resolve_project_title(script, "  User's Title  ") == "User's Title"
    script.write_text("Nia watches the harbor clock.")
    assert runner.resolve_project_title(script) == "The Tide Clock"
    assert runner.resolve_project_title(None) == "Untitled Comic"


def test_live_partial_failure_report_uses_durable_ledger_without_live_requests(
    monkeypatch, tmp_path
):
    stub_managed_stack(monkeypatch)
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
        ledger.finish_call(index, 500, uncertain=True)
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
    assert report["accounted_spend_usd"] == 0.01029325
    assert report["cumulative_accounted_spend_usd"] == 0.02729925
    assert report["uncertain_amount_usd"] == 0.01
    assert report["cost_complete"] is False
    assert report["status"] == "failed" and report["pdf_path"] is None
    assert len(report["diagnostic_files"]) == 1
    assert Path(report["diagnostic_files"][0]).parent == Path(report["diagnostics_dir"])


def test_artifacts_inside_repository_are_ignored(monkeypatch, tmp_path):
    stub_managed_stack(monkeypatch)
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


def test_resume_requires_explicit_existing_stack_attachment(tmp_path):
    assert (
        runner.main(
            [
                "--resume-project-id",
                "project-1",
                "--resume-user-id",
                "user-1",
                "--script-file",
                str(tmp_path / "story.txt"),
                "--artifact-dir",
                str(tmp_path / "artifacts"),
            ]
        )
        == 2
    )


@pytest.mark.parametrize(
    "explicit_retry,legacy",
    [
        (False, None),
        (True, None),
        (False, "matching"),
        (True, "matching"),
        (True, "wrong-job"),
        (True, "priced"),
        (True, "orphan"),
        (True, "completed"),
        (True, "superseded"),
        (True, "wrong-entity"),
    ],
)
def test_resume_reuses_persisted_panels_and_requires_explicit_new_attempt(
    monkeypatch, tmp_path, explicit_retry, legacy
):
    source = "Nia guides a boat home at sunrise."
    script_file = tmp_path / "story.txt"
    script_file.write_text(source)
    panel_ids = [f"panel-{index}" for index in range(1, 5)]
    page = {
        "id": "page-1",
        "panel_ids": panel_ids,
        "draft_image_job_id": "uncertain-1",
    }
    if legacy == "orphan":
        page.pop("draft_image_job_id")
    elif legacy == "completed":
        page["draft_image_url"] = "http://local/storage/fake/draft"
    elif legacy == "superseded":
        page["draft_image_job_id"] = "newer-job"
    chapter = {"id": "chapter-1", "status": "approved"}
    calls = []
    fake_colouring = types.ModuleType("colouring")
    fake_colouring.colour_enclosed_regions = lambda image, palette: b"fake png"
    monkeypatch.setitem(sys.modules, "colouring", fake_colouring)
    monkeypatch.setattr(runner, "fetch_line_art_image", lambda _: b"fake line art")

    def request(path, method="GET", payload=None, extra_headers=None):
        if method != "GET":
            calls.append((method, path, extra_headers))
        if path == "/api/v1/projects/project-1":
            return {
                "project": {
                    "id": "project-1",
                    "user_id": "user-1",
                    "original_script": source,
                    "chapter_count": 1,
                    "art_style": "storybook",
                }
            }
        if path.endswith("/characters"):
            return {
                "characters": [
                    {
                        "id": "character-1",
                        "name": "Nia",
                        "visual_attributes": {"reference_role": "extra"},
                        "approved_at": "saved",
                    }
                ]
            }
        if path.endswith("/chapters"):
            return {"chapters": [chapter]}
        if path.endswith("/panels"):
            return {"panels": [{"id": panel_id} for panel_id in panel_ids]}
        if path.endswith("/pages"):
            return {"pages": [page | {"line_art_image_url": "http://local/storage/fake/art"}]}
        if path == "/api/v1/pages/page-1/colour-palette":
            return {"palette": {}}
        if path == "/api/v1/generation-jobs/uncertain-1":
            return {
                "job": {
                    "id": "uncertain-1",
                    "status": "failed" if legacy else "uncertain",
                    "project_id": "project-1",
                    "job_type": "draft_sketch",
                    "entity_id": "another-page"
                    if legacy == "wrong-entity"
                    else "page-1"
                    if legacy in {"completed", "superseded"}
                    else None,
                }
            }
        if path == "/api/v1/generation-jobs/job-1":
            return {"job": {"status": "done", "actual_cost": 0.01}}
        if path == "/api/v1/exports/export-1":
            return {"export": {"status": "completed"}}
        if path == "/api/v1/exports/export-1/download":
            return b"%PDF-1.7 /Type /Page /Type /Page"
        if path == "/api/v1/projects/project-1/export":
            return {"export": {"id": "export-1"}}
        if path.endswith(("/draft", "/line-art")):
            return {"job_id": "job-1"}
        if path.endswith("/suggest"):
            return {"palette": {"sea": "#123456"}}
        return {}

    monkeypatch.setattr(runner, "json_request", request)
    monkeypatch.setattr(runner, "binary_request", lambda *args: {})
    guard = runner.CostGuard(float("inf"), live=False)
    guard.script_file = script_file
    if legacy:
        guard.provider_report = {
            "accounted_spend_usd": 0.01,
            "calls": [
                {
                    "job_id": "other-job" if legacy == "wrong-job" else "uncertain-1",
                    "stage": "image",
                    "status": "uncertain",
                    "actual_cost_usd": 0.01 if legacy == "priced" else None,
                }
            ],
        }
    if legacy in {"wrong-job", "priced"}:
        with pytest.raises(runner.E2EError, match="is not uncertain"):
            runner.run_flow(
                guard,
                tmp_path,
                resume_project_id="project-1",
                resume_user_id="user-1",
                retry_uncertain_job_id="uncertain-1",
            )
        assert not calls
        return
    if legacy in {"orphan", "completed", "superseded", "wrong-entity"}:
        with pytest.raises(runner.E2EError, match="retry refused"):
            runner.run_flow(
                guard,
                tmp_path,
                resume_project_id="project-1",
                resume_user_id="user-1",
                retry_uncertain_job_id="uncertain-1",
            )
        assert not calls  # Refuse before approvals, moderation, repaints or images.
        return
    if explicit_retry:
        count, project_id = runner.run_flow(
            guard,
            tmp_path / "artifacts",
            resume_project_id="project-1",
            resume_user_id="user-1",
            retry_uncertain_job_id="uncertain-1",
        )
    else:
        with pytest.raises(runner.E2EError, match="uncertain provider outcome"):
            runner.run_flow(
                guard,
                tmp_path / "artifacts",
                resume_project_id="project-1",
                resume_user_id="user-1",
            )
        assert not any(path == "/api/v1/pages/page-1/draft" for _, path, _ in calls)
        return
    assert (count, project_id) == (1, "project-1")
    provider_actions = [path for method, path, _ in calls if method == "POST"]
    assert "/api/v1/projects/project-1/analyze" not in provider_actions
    assert "/api/v1/projects/project-1/chapters" not in provider_actions
    assert "/api/v1/chapters/chapter-1/panels" not in provider_actions
    assert provider_actions[0] == "/api/v1/characters/character-1/approve"
    retry_requests = [item for item in calls if item[1] == "/api/v1/pages/page-1/draft"]
    assert len(retry_requests) == 1
    assert retry_requests[0][2]["Idempotency-Key"]
    assert "/api/v1/pages/page-1/line-art" not in provider_actions


def test_resume_refuses_script_mismatch_before_project_mutation(monkeypatch, tmp_path):
    script_file = tmp_path / "story.txt"
    script_file.write_text("new script")
    calls = []

    def request(path, method="GET", payload=None, extra_headers=None):
        calls.append((method, path))
        return {
            "project": {
                "id": "project-1",
                "original_script": "different stored script",
            }
        }

    monkeypatch.setattr(runner, "json_request", request)
    guard = runner.CostGuard(float("inf"), live=False)
    guard.script_file = script_file
    with pytest.raises(runner.E2EError, match="script file differs"):
        runner.run_flow(
            guard,
            tmp_path,
            resume_project_id="project-1",
            resume_user_id="user-1",
        )
    assert calls == [("GET", "/api/v1/projects/project-1")]


@pytest.mark.parametrize("draft_approved,selected", [(True, 1), (False, 1), (True, 2)])
def test_explicit_repaint_reuses_draft_and_buys_only_a_painted_finish(
    monkeypatch, tmp_path, draft_approved, selected
):
    source = "Nia guides the boat home."
    script = tmp_path / "story.txt"
    script.write_text(source)
    ids = [f"panel-{n}" for n in range(4)]
    page = {
        "id": "page-1",
        "panel_ids": ids,
        "draft_image_url": "http://local/approved-draft",
        "draft_approved_at": "saved" if draft_approved else None,
        "line_art_image_url": "http://local/old-ink",
        "line_art_image_job_id": "old-ink-job",
    }
    calls = []

    def request(path, method="GET", payload=None, extra_headers=None):
        if method != "GET":
            calls.append((method, path))
        if path == "/api/v1/projects/project-1":
            return {"project": {"id": "project-1", "user_id": "user-1", "original_script": source}}
        if path.endswith("/characters"):
            return {
                "characters": [
                    {
                        "id": "nia",
                        "name": "Nia",
                        "approved_at": "saved",
                        "reference_image_url": "http://local/reference",
                        "visual_attributes": {},
                    }
                ]
            }
        if path.endswith("/chapters"):
            return {"chapters": [{"id": "chapter-1", "status": "approved"}]}
        if path.endswith("/panels"):
            return {"panels": [{"id": i} for i in ids]}
        if path.endswith("/pages"):
            return {"pages": [page]}
        if path.endswith("/old-ink-job"):
            return {"job": {"status": "done"}}
        if path.endswith("/new-paint-job"):
            return {"job": {"status": "done", "actual_cost": 0.01}}
        if path.endswith("/line-art"):
            assert "variant=ink" not in path
            return {"job_id": "new-paint-job"}
        if path.endswith("/export"):
            return {"export": {"id": "export-1"}}
        if path.endswith("/export-1"):
            return {"export": {"status": "completed"}}
        if path.endswith("/download"):
            return b"%PDF-1.7 /Type /Page /Type /Page"
        return {}

    monkeypatch.setattr(runner, "json_request", request)
    guard = runner.CostGuard(float("inf"), live=False)
    guard.script_file = script
    kwargs = {
        "resume_project_id": "project-1",
        "resume_user_id": "user-1",
        "repaint_pages": {selected},
    }
    if not draft_approved or selected > 1:
        with pytest.raises(runner.E2EError, match="approved draft|outside this comic"):
            runner.run_flow(guard, tmp_path / "artifacts", **kwargs)
        assert guard.actual == 0
        return
    count, project = runner.run_flow(guard, tmp_path / "artifacts", **kwargs)
    assert (count, project) == (1, "project-1")
    assert guard.actual == 0.01
    assert page["line_art_image_url"] == "http://local/old-ink"
    assert ("POST", "/api/v1/pages/page-1/line-art") in calls
    assert not any(
        path.endswith(("/draft", "/analyze", "/reference-image", "/suggest")) for _, path in calls
    )
    assert not any("variant=ink" in path or method == "DELETE" for method, path in calls)


@pytest.mark.parametrize("pages", [["0"], ["1", "1"], ["-1"]])
def test_invalid_repaint_selection_is_refused_before_services(monkeypatch, pages):
    monkeypatch.setattr(runner, "ensure_services", lambda: pytest.fail("must not start services"))
    assert runner.main(["--resume-project-id", "project-1", "--repaint-pages", *pages]) == 2


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
    stub_managed_stack(monkeypatch)

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
