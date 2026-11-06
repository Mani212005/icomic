"""E2E quota/port overrides are isolated and visible; default deployment is unchanged."""

import importlib.util
import json
from pathlib import Path

import yaml

ROOT = Path(__file__).resolve().parents[3]
spec = importlib.util.spec_from_file_location("e2e_quota_runner", ROOT / "scripts/e2e/comic_e2e.py")
runner = importlib.util.module_from_spec(spec)
spec.loader.exec_module(runner)


class ComposeLoader(yaml.SafeLoader):
    pass


ComposeLoader.add_constructor(
    "!reset",
    lambda loader, node: (
        loader.construct_sequence(node) if isinstance(node, yaml.SequenceNode) else None
    ),
)
ComposeLoader.add_constructor("!override", lambda loader, node: loader.construct_sequence(node))


def test_default_deployment_has_no_e2e_quota_override():
    default = yaml.safe_load((ROOT / "deploy/docker-compose.yml").read_text())
    assert not any(
        key.startswith("ICOMIC_E2E_") for key in default["services"]["backend"]["environment"]
    )
    overlay = yaml.load((ROOT / "deploy/docker-compose.e2e.yml").read_text(), Loader=ComposeLoader)
    env = overlay["services"]["backend"]["environment"]
    assert env == {
        "ICOMIC_E2E_ISOLATED_STACK": "1",
        "ICOMIC_E2E_DAILY_IMAGES": "${ICOMIC_E2E_DAILY_IMAGES:-100}",
        "ICOMIC_E2E_MONTHLY_IMAGES": "${ICOMIC_E2E_MONTHLY_IMAGES:-1000}",
    }
    assert all(service["container_name"] is None for service in overlay["services"].values())
    assert overlay["services"]["postgres"]["ports"] == []
    assert overlay["services"]["redis"]["ports"] == []
    assert overlay["services"]["backend"]["ports"] == ["127.0.0.1::8080"]
    assert overlay["services"]["ai"]["ports"] == ["127.0.0.1::8000"]


def test_managed_run_reports_its_unique_stack_and_limits(monkeypatch, tmp_path, capsys):
    monkeypatch.delenv("ICOMIC_E2E_DAILY_IMAGES", raising=False)
    monkeypatch.delenv("ICOMIC_E2E_MONTHLY_IMAGES", raising=False)
    projects = []
    monkeypatch.setattr(runner, "ensure_services", lambda: projects.append(runner.COMPOSE_PROJECT))
    monkeypatch.setattr(runner, "warn_stale_e2e_projects", lambda project: [])
    cleaned = []
    monkeypatch.setattr(runner, "teardown_managed_stack", lambda project: cleaned.append(project))
    monkeypatch.setattr(runner, "run_flow", lambda *args: (0, "synthetic-project"))
    for n in range(2):
        output = tmp_path / str(n)
        assert runner.main(["--artifact-dir", str(output)]) == 0
        policy = json.loads((output / "cost-report.json").read_text())["quota_policy"]
        assert policy["daily_image_limit"] == 100 and policy["monthly_image_limit"] == 1000
        assert policy["scope"] == "isolated_e2e_stack"
        assert policy["compose_project"] == projects[-1]
        command = runner.compose_command()
        assert "deploy/docker-compose.e2e.yml" in command and projects[-1] in command
    assert projects[0] != projects[1]
    assert cleaned == projects
    assert "daily=100 monthly=1000" in capsys.readouterr().out


def test_managed_stack_teardown_targets_only_its_compose_project(monkeypatch):
    project = "icomic-e2e-owned-run"
    monkeypatch.setattr(runner, "COMPOSE_PROJECT", project)
    commands = []

    def fake_run(command, **kwargs):
        commands.append((command, kwargs))

    runner.teardown_managed_stack(project, run=fake_run)

    command, kwargs = commands[0]
    assert command[-2:] == ["down", "-v"]
    assert project in command
    assert kwargs["check"] is True


def test_stale_project_warning_lists_only_old_managed_projects(capsys):
    now = runner.datetime(2026, 11, 6, tzinfo=runner.timezone.utc)
    outputs = iter(
        [
            "old-id\nrecent-id\ncurrent-id\nother-id\n",
            (
                "icomic-e2e-old\t2026-11-04T00:00:00Z\n"
                "icomic-e2e-recent\t2026-11-05T12:00:00Z\n"
                "icomic-e2e-current\t2026-11-04T00:00:00Z\n"
                "deploy_default\t2026-11-01T00:00:00Z\n"
            ),
        ]
    )
    commands = []

    def fake_run(command, **kwargs):
        commands.append(command)
        return type("Result", (), {"stdout": next(outputs)})()

    stale = runner.warn_stale_e2e_projects("icomic-e2e-current", now=now, run=fake_run)

    assert stale == ["icomic-e2e-old"]
    assert "icomic-e2e-old" in capsys.readouterr().err
    assert len(commands) == 2


def test_external_service_run_leaves_quota_unchanged(monkeypatch, tmp_path):
    monkeypatch.setattr(
        runner,
        "ensure_services",
        lambda: (_ for _ in ()).throw(AssertionError("must not start services")),
    )
    monkeypatch.setattr(runner, "run_flow", lambda *args: (0, "synthetic-project"))
    assert runner.main(["--skip-services", "--artifact-dir", str(tmp_path)]) == 0
    policy = json.loads((tmp_path / "cost-report.json").read_text())["quota_policy"]
    assert policy == {"scope": "external_services", "limits": "unchanged"}
    assert "deploy/docker-compose.e2e.yml" not in runner.compose_command()


def test_keep_stack_skips_managed_teardown(monkeypatch, tmp_path):
    monkeypatch.setattr(runner, "warn_stale_e2e_projects", lambda project: [])
    monkeypatch.setattr(runner, "ensure_services", lambda: None)
    monkeypatch.setattr(runner, "run_flow", lambda *args: (0, "synthetic-project"))
    monkeypatch.setattr(
        runner,
        "teardown_managed_stack",
        lambda project: (_ for _ in ()).throw(AssertionError("stack must be kept")),
    )

    assert runner.main(["--keep-stack", "--artifact-dir", str(tmp_path)]) == 0
    report = json.loads((tmp_path / "cost-report.json").read_text())
    assert report["stack_kept"] is True


def test_failed_managed_run_still_tears_down_its_stack(monkeypatch, tmp_path):
    monkeypatch.setattr(runner, "warn_stale_e2e_projects", lambda project: [])
    monkeypatch.setattr(runner, "ensure_services", lambda: None)
    cleaned = []

    def fail_flow(*args):
        raise runner.E2EError("synthetic failure")

    monkeypatch.setattr(runner, "run_flow", fail_flow)
    monkeypatch.setattr(runner, "teardown_managed_stack", lambda project: cleaned.append(project))

    assert runner.main(["--artifact-dir", str(tmp_path)]) == 1
    assert len(cleaned) == 1 and cleaned[0].startswith("icomic-e2e-")


def test_live_overlay_flag_reaches_isolated_runner_without_fake_only_refusal(
    monkeypatch, tmp_path, capsys
):
    monkeypatch.setenv("ICOMIC_E2E_LIVE", "1")
    for name in ("LANGUAGE", "VISION", "IMAGE"):
        monkeypatch.setenv(f"ADAPTER_{name}", "meta")
    monkeypatch.setattr(runner, "live_authorization_error", lambda *args: None)
    monkeypatch.setattr(runner, "warn_stale_e2e_projects", lambda project: [])

    def start_fake_services():
        run_id = runner.os.environ["ICOMIC_E2E_RUN_ID"]
        reserve = runner.calculate_checklist_worst_case(runner.DEFAULT_REASONING_RESERVE_TOKENS)
        (tmp_path / f"provider-costs-{run_id}.json").write_text(
            json.dumps(
                {
                    "actual_spend_usd": 0,
                    "accounted_spend_usd": 0,
                    "hard_cap_usd": 2,
                    "prior_spend_usd": 0,
                    "third_check_reserve_usd": round(reserve, 9),
                    "calls": [],
                    "unpriced_calls": 0,
                    "uncertain_amount_usd": 0,
                }
            )
        )

    monkeypatch.setattr(runner, "ensure_services", start_fake_services)
    monkeypatch.setattr(runner, "teardown_managed_stack", lambda project: None)
    calls = []

    def stop_before_provider_call(guard, artifact_dir):
        calls.append((guard.live, guard.overlay_lettering))
        raise runner.E2EError("synthetic stop before provider calls")

    monkeypatch.setattr(runner, "run_flow", stop_before_provider_call)

    assert (
        runner.main(
            [
                "--live",
                "--cost-cap-usd",
                "2",
                "--overlay-lettering",
                "--artifact-dir",
                str(tmp_path),
            ]
        )
        == 1
    )

    assert calls == [(True, True)]
    assert "fake-only" not in capsys.readouterr().err
