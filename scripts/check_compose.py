#!/usr/bin/env python3
"""Render the Compose files and check the promises a personal local run depends on.

`docker compose config` already rejects invalid YAML, unknown keys and unresolved
variables. This adds the invariants that are easy to lose in an edit and costly to
discover late: generated images and the spend ledger sit on a named volume, overlay
lettering and the spend cap default on, readiness gates startup, and the isolated E2E
overlay never claims a fixed container name or host port.

Usage: python3 scripts/check_compose.py   (needs the docker compose plugin, no daemon)
"""

from __future__ import annotations

import json
import subprocess
import sys
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parent.parent
DEPLOY = REPO_ROOT / "deploy"
# Values the production overlay requires from its environment; any placeholder will do.
PROD_ENV = {
    "DATABASE_URL": "postgres://u:p@db:5432/icomic",
    "REDIS_URL": "redis://redis:6379/0",
    "PUBLIC_API_URL": "https://api.example.invalid",
}


def render(files: list[str], env: dict[str, str] | None = None) -> dict:
    command = ["docker", "compose"]
    for name in files:
        command += ["-f", str(DEPLOY / name)]
    command += ["config", "--format", "json"]
    full_env = {"PATH": "/usr/local/bin:/usr/bin:/bin", "HOME": str(Path.home()), **(env or {})}
    done = subprocess.run(command, capture_output=True, text=True, env=full_env, check=False)
    if done.returncode:
        raise SystemExit(f"docker compose config failed for {files}:\n{done.stderr.strip()}")
    return json.loads(done.stdout)


def _named_volume_for(service: dict, config: dict, path: str) -> str | None:
    """The top-level named volume that holds `path` in this service, if any."""
    best = None
    for mount in service.get("volumes", []):
        target = mount["target"].rstrip("/")
        if mount["type"] == "volume" and (path == target or path.startswith(target + "/")):
            if best is None or len(target) > len(best[0]):
                best = (target, mount["source"])
    if best and best[1] in config.get("volumes", {}):
        return best[1]
    return None


def check_base(config: dict) -> list[str]:
    problems = []
    services = config.get("services", {})
    ai, backend = services.get("ai", {}), services.get("backend", {})
    env = ai.get("environment", {})
    if env.get("ADAPTER_STORAGE") != "local":
        problems.append("ai must default to ADAPTER_STORAGE=local so images survive restarts")
    for variable in ("LOCAL_STORAGE_DIR", "PROVIDER_COST_LEDGER_PATH"):
        path = env.get(variable, "")
        if not path or _named_volume_for(ai, config, path) is None:
            problems.append(f"ai {variable}={path!r} must sit on a named volume, or a restart loses it")
    if not env.get("PROVIDER_SPEND_CAP_USD"):
        problems.append("ai must default PROVIDER_SPEND_CAP_USD to a cap")
    for name, service in (("ai", ai), ("backend", backend)):
        if service.get("environment", {}).get("LETTERING_OVERLAY_ENABLED") != "true":
            problems.append(f"{name} must default LETTERING_OVERLAY_ENABLED to true")
        probe = " ".join(service.get("healthcheck", {}).get("test", []))
        if "/readyz" not in probe:
            problems.append(f"{name} health check must probe /readyz, not liveness: {probe!r}")
    depends = backend.get("depends_on", {})
    if depends.get("ai", {}).get("condition") != "service_healthy":
        problems.append("backend must wait for a healthy (ready) ai service")
    if depends.get("postgres", {}).get("condition") != "service_healthy":
        problems.append("backend must wait for a healthy postgres")
    return problems


def check_e2e_overlay(config: dict) -> list[str]:
    problems = []
    for name, service in config.get("services", {}).items():
        if service.get("container_name"):
            problems.append(f"{name}: the E2E overlay must not set a fixed container_name")
        for port in service.get("ports", []):
            if not port.get("host_ip") or not str(port.get("published", "")).strip() in ("", "0"):
                problems.append(f"{name}: the E2E overlay must publish only ephemeral loopback ports")
    return problems


def check_prod_overlay(config: dict) -> list[str]:
    env = config.get("services", {}).get("ai", {}).get("environment", {})
    if env.get("ADAPTER_STORAGE") != "local":
        return ["the production overlay must keep ADAPTER_STORAGE=local by default"]
    return []


def main() -> int:
    problems = []
    base = render(["docker-compose.yml"])
    problems += check_base(base)
    problems += check_e2e_overlay(render(["docker-compose.yml", "docker-compose.e2e.yml"]))
    problems += check_prod_overlay(render(["docker-compose.yml", "docker-compose.prod.yml"], PROD_ENV))
    render(["docker-compose.realdb.yml"])
    off = render(["docker-compose.yml"], {"PROVIDER_SPEND_CAP_USD": ""})
    if off["services"]["ai"]["environment"].get("PROVIDER_SPEND_CAP_USD") != "":
        problems.append("setting PROVIDER_SPEND_CAP_USD empty must turn the cap off, not fall back to the default")
    if problems:
        print("Compose check failed:", *problems, sep="\n  - ", file=sys.stderr)
        return 1
    print("Compose files render and keep the personal-run invariants.")
    return 0


if __name__ == "__main__":
    sys.exit(main())
