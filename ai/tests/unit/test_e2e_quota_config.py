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
    assert "daily=100 monthly=1000" in capsys.readouterr().out


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
