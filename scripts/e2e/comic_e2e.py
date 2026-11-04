#!/usr/bin/env python3
"""Run the full comic flow against the local Go API and configured AI adapters.

Usage: make e2e-comic [E2E_ARGS="--live --cost-cap-usd 2"]
The default adapter configuration is fake. Live use requires --live, ICOMIC_E2E_LIVE=1,
and --cost-cap-usd 2; all adapter selection still comes from ADAPTER_* environment.
"""

from __future__ import annotations

import argparse
import json
import math
import os
import re
import shutil
import subprocess
import sys
import time
import uuid
from datetime import datetime, timezone
from pathlib import Path
from typing import Any
from urllib.error import HTTPError, URLError
from urllib.request import Request, urlopen

REPO_ROOT = Path(__file__).resolve().parents[2]
ARTIFACT_DIR = Path("/home/mani/icomic-artifacts/comic-e2e")
CAP_USD = 2.0
DEV_USER_ID = "00000000-0000-0000-0000-000000000001"
API_URL = os.environ.get("ICOMIC_E2E_API_URL", "http://127.0.0.1:8080").rstrip("/")

AI_PATH = REPO_ROOT / "ai"
sys.path.insert(0, str(AI_PATH))
from app.costs import (
    DEFAULT_REASONING_RESERVE_TOKENS,
    calculate_checklist_worst_case,
    estimate_call_worst_case,
)


class E2EError(RuntimeError):
    pass


def live_authorization_error(
    live_flag: bool,
    environ: dict[str, str],
    cost_cap: float | None,
) -> str | None:
    if environ.get("CI") or environ.get("PYTEST_CURRENT_TEST"):
        return "live mode is disabled in CI and tests"
    if not live_flag:
        return "live mode requires the --live flag"
    if environ.get("ICOMIC_E2E_LIVE") != "1":
        return "live mode requires ICOMIC_E2E_LIVE=1"
    if cost_cap != CAP_USD:
        return "live mode requires --cost-cap-usd 2"
    return None


class CostGuard:
    """Reserve the third planned live check and reject before over-budget calls."""

    def __init__(
        self,
        cap: float,
        live: bool,
        reserve_tokens: int = DEFAULT_REASONING_RESERVE_TOKENS,
        ledger_path: Path | None = None,
        prior_spend: float = 0.0,
    ):
        self.cap = cap
        self.live = live
        self.actual = 0.0
        self.stages: list[dict[str, Any]] = []
        self.ledger_path = ledger_path
        self.prior_spend = prior_spend
        self.provider_report: dict[str, Any] | None = None
        self.current_stage: str | None = None
        self.project_id: str | None = None
        self.reserve_tokens = reserve_tokens if live else 0
        self.third_check_reserve = (
            calculate_checklist_worst_case(self.reserve_tokens) if live else 0.0
        )

    def before_call(self, stage: str, count: int = 1) -> float:
        self.current_stage = stage
        self.refresh()
        estimate = (
            estimate_call_worst_case(stage, self.reserve_tokens) * count if self.live else 0.0
        )
        spent = self.provider_report["accounted_spend_usd"] if self.provider_report else self.actual
        if self.prior_spend + spent + estimate + self.third_check_reserve > self.cap + 1e-12:
            raise E2EError(
                f"cost guard refused {stage}: actual ${self.actual:.6f} + "
                f"worst-case ${estimate:.6f} + third-check reserve "
                f"${self.third_check_reserve:.6f} exceeds ${self.cap:.2f}"
            )
        return estimate

    def refresh(self) -> None:
        if self.ledger_path is None:
            return
        if not self.ledger_path.exists():
            raise E2EError("Provider cost ledger is unavailable; refusing further calls")
        try:
            report = json.loads(self.ledger_path.read_text())
            for name in ("actual_spend_usd", "accounted_spend_usd"):
                if not math.isfinite(report[name]) or report[name] < 0:
                    raise ValueError("invalid cost")
            if (
                report["hard_cap_usd"] != self.cap
                or report["prior_spend_usd"] != self.prior_spend
                or report["third_check_reserve_usd"] != round(self.third_check_reserve, 9)
            ):
                raise ValueError("budget mismatch")
            self.provider_report = report
            self.actual = report["actual_spend_usd"]
            self.stages = report["calls"]
        except (KeyError, TypeError, ValueError, OSError) as exc:
            raise E2EError("Provider cost ledger is invalid; refusing further calls") from exc

    def record(self, stage: str, job_id: str | None, usage_cost: float, estimate: float) -> None:
        if self.ledger_path is not None:
            self.refresh()
            print(
                f"Cumulative observed spend: ${self.prior_spend + self.actual:.9f} USD after {stage}"
            )
            return
        amount = max(0.0, usage_cost)
        if self.live and stage in {"language", "vision", "image"} and amount <= 0:
            raise E2EError(f"live {stage} call returned no billable usage record; aborting")
        self.actual += amount
        if self.actual + self.third_check_reserve > self.cap + 1e-12:
            raise E2EError("recorded usage exceeded the hard cap reserve; aborting")
        self.stages.append(
            {
                "stage": stage,
                "job_id": job_id,
                "actual_cost_usd": round(amount, 9),
                "worst_case_estimate_usd": round(estimate, 9),
                "cumulative_spend_usd": round(self.actual, 9),
            }
        )
        print(f"Cumulative spend: ${self.actual:.6f} USD after {stage}")


def json_request(
    path: str,
    method: str = "GET",
    payload: dict[str, Any] | None = None,
) -> Any:
    data = json.dumps(payload).encode() if payload is not None else None
    request = Request(
        f"{API_URL}{path}",
        data=data,
        method=method,
        headers={"X-User-ID": DEV_USER_ID, "Content-Type": "application/json"},
    )
    try:
        with urlopen(request, timeout=60) as response:
            body = response.read()
            if "application/pdf" in response.headers.get("Content-Type", ""):
                return body
            return json.loads(body) if body else {}
    except HTTPError as exc:
        detail = exc.read().decode("utf-8", errors="replace")
        raise E2EError(f"{method} {path} returned HTTP {exc.code}: {detail}") from exc
    except URLError as exc:
        raise E2EError(f"{method} {path} failed: {exc.reason}") from exc


def checked_call(
    guard: CostGuard,
    stage: str,
    count: int,
    method: str,
    path: str,
    payload: dict[str, Any] | None = None,
) -> Any:
    estimate = guard.before_call(stage, count)
    result = json_request(path, method, payload)
    job_id = None
    if isinstance(result, dict):
        job_record = result.get("job")
        job_id = result.get("job_id") or (
            job_record.get("id") if isinstance(job_record, dict) else None
        )
    actual = 0.0
    if job_id:
        job = json_request(f"/api/v1/generation-jobs/{job_id}").get("job", {})
        if job.get("status") != "done":
            raise E2EError(f"{stage} usage record did not complete: {job.get('status')}")
        actual = float(job.get("actual_cost") or 0.0)
    guard.record(stage, job_id, actual, estimate)
    return result


def wait_for_api(timeout: float = 180.0) -> None:
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        try:
            json_request("/healthz")
            return
        except E2EError:
            time.sleep(2)
    raise E2EError("Go API did not become healthy within 180 seconds")


def ensure_services() -> None:
    command = [
        "docker",
        "compose",
        "-f",
        "deploy/docker-compose.yml",
        "up",
        "-d",
        "--build",
        "postgres",
        "redis",
        "ai",
        "backend",
    ]
    subprocess.run(command, cwd=REPO_ROOT, check=True)
    wait_for_api()


def require(mapping: dict[str, Any], key: str, message: str) -> Any:
    value = mapping.get(key)
    if value is None or value == "":
        raise E2EError(message)
    return value


def verify_pdf(pdf: bytes, expected_comic_pages: int) -> None:
    if not pdf or not pdf.startswith(b"%PDF-"):
        raise E2EError("download is empty or missing the PDF header")
    page_count = len(re.findall(rb"/Type\s*/Page\b", pdf))
    if page_count < expected_comic_pages:
        raise E2EError(
            f"PDF contains {page_count} pages; expected at least {expected_comic_pages} generated pages"
        )


def run_flow(guard: CostGuard, artifact_dir: Path = ARTIFACT_DIR) -> tuple[int, str]:
    usage = json_request("/api/v1/me/credit-usage")
    if float(usage.get("balance", 0)) < 10:
        raise E2EError("dev account has insufficient starting credits")

    script = (
        "Nia, a young lighthouse keeper, discovers a tiny brass bird during a storm. "
        "The bird lights the dark sea, guiding a lost boat safely home. At sunrise, Nia "
        "sets it free and watches it fly toward the bright horizon."
    )
    project_response = json_request(
        "/api/v1/projects",
        "POST",
        {
            "title": f"Lighthouse Bird {datetime.now(timezone.utc).strftime('%Y%m%dT%H%M%SZ')}",
            "original_script": script,
            "art_style": "storybook",
            "chapter_count": 1,
        },
    )
    project = require(project_response, "project", "project creation response has no project")
    project_id = require(project, "id", "created project has no id")
    guard.project_id = project_id
    print(f"PASS project created: {project_id}")

    analysis = checked_call(guard, "language", 1, "POST", f"/api/v1/projects/{project_id}/analyze")
    characters = analysis.get("character_profiles", [])
    if not characters:
        raise E2EError("script analysis returned no characters")
    print(f"PASS script written and analyzed: {len(characters)} characters")

    character_list = json_request(f"/api/v1/projects/{project_id}/characters").get("characters", [])
    if len(character_list) != len(characters):
        raise E2EError("character list does not match script analysis")
    for character in character_list:
        cid = require(character, "id", "generated character has no id")
        checked_call(guard, "image", 1, "POST", f"/api/v1/characters/{cid}/reference-image")
        json_request(f"/api/v1/characters/{cid}/approve", "POST")
    print(f"PASS character references generated and approved: {len(character_list)}")

    outline = checked_call(guard, "language", 1, "POST", f"/api/v1/projects/{project_id}/chapters")
    chapters = outline.get("chapters", [])
    if not chapters:
        raise E2EError("chapter generation returned no chapters")
    for chapter in chapters:
        json_request(f"/api/v1/chapters/{chapter['id']}/approve", "POST")
    chapter_id = chapters[0]["id"]
    print(f"PASS chapter outline generated and approved: {len(chapters)}")

    panel_response = checked_call(
        guard, "language", 1, "POST", f"/api/v1/chapters/{chapter_id}/panels"
    )
    panels = panel_response.get("panels", [])
    pages = panel_response.get("pages", [])
    if not panels or not pages:
        raise E2EError("panel generation did not produce panels and pages")
    print(f"PASS comic pages generated: {len(panels)} panels across {len(pages)} pages")

    for page in pages:
        page_id = page["id"]
        checked_call(guard, "image", 1, "POST", f"/api/v1/pages/{page_id}/draft")
        json_request(f"/api/v1/pages/{page_id}/draft/approve", "POST")
        checked_call(guard, "image", 1, "POST", f"/api/v1/pages/{page_id}/line-art")
        json_request(f"/api/v1/pages/{page_id}/line-art/approve", "POST")
        checked_call(
            guard,
            "vision",
            1,
            "POST",
            f"/api/v1/pages/{page_id}/colour-palette/suggest",
        )
        json_request(f"/api/v1/pages/{page_id}/colour/approve", "POST")
    print(f"PASS pages inked and coloured: {len(pages)}")

    guard.current_stage = "export"
    exported = json_request(f"/api/v1/projects/{project_id}/export", "POST")
    export = require(exported, "export", "export response has no export record")
    export_id = require(export, "id", "export response has no id")
    export_status = json_request(f"/api/v1/exports/{export_id}").get("export", {})
    if export_status.get("status") != "completed":
        raise E2EError(f"export did not complete: {export_status.get('status')}")
    pdf = json_request(f"/api/v1/exports/{export_id}/download")
    if not isinstance(pdf, bytes):
        raise E2EError("download endpoint did not return PDF bytes")
    verify_pdf(pdf, len(pages))
    artifact_dir.mkdir(parents=True, exist_ok=True)
    timestamp = datetime.now(timezone.utc).strftime("%Y%m%dT%H%M%S%fZ")
    staged = artifact_dir / f"latest-{timestamp}.pdf"
    staged.write_bytes(pdf)
    shutil.copyfile(staged, artifact_dir / "latest.pdf")
    print(f"PASS export and PDF verified: {len(pages)} generated page(s)")
    return len(pages), project_id


def parse_args(argv: list[str] | None = None) -> argparse.Namespace:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--live",
        action="store_true",
        help="explicitly authorize configured real adapters",
    )
    parser.add_argument(
        "--cost-cap-usd",
        type=float,
        help="required hard cap for live mode (exactly 2 USD)",
    )
    parser.add_argument(
        "--skip-services",
        action="store_true",
        help="use already-running local services",
    )
    parser.add_argument(
        "--prior-spend-usd",
        type=float,
        default=0.0,
        help="previously accounted spend deducted from the shared live cap",
    )
    parser.add_argument(
        "--artifact-dir",
        type=Path,
        default=ARTIFACT_DIR,
        help="directory for the verified PDF and cost report",
    )
    return parser.parse_args(argv)


def main(argv: list[str] | None = None) -> int:
    args = parse_args(argv)
    if args.live:
        error = live_authorization_error(args.live, dict(os.environ), args.cost_cap_usd)
        if error:
            print(f"REFUSED: {error}", file=sys.stderr)
            return 2
        configured = [
            os.environ.get(f"ADAPTER_{name}", "fake").lower()
            for name in ("LANGUAGE", "VISION", "IMAGE")
        ]
        if configured != ["meta", "meta", "meta"]:
            print(
                "REFUSED: live mode requires ADAPTER_LANGUAGE, ADAPTER_VISION, and ADAPTER_IMAGE=meta",
                file=sys.stderr,
            )
            return 2
    elif args.cost_cap_usd is not None:
        print(
            "REFUSED: --cost-cap-usd is accepted only together with --live",
            file=sys.stderr,
        )
        return 2
    else:
        configured = [
            os.environ.get(f"ADAPTER_{name}", "fake").lower()
            for name in ("LANGUAGE", "VISION", "IMAGE")
        ]
        if configured != ["fake", "fake", "fake"]:
            print(
                "REFUSED: non-fake adapters require all live-mode safeguards",
                file=sys.stderr,
            )
            return 2

    live = bool(args.live)
    if (
        not math.isfinite(args.prior_spend_usd)
        or args.prior_spend_usd < 0
        or (not live and args.prior_spend_usd)
    ):
        print(
            "REFUSED: --prior-spend-usd must be a finite non-negative live spend", file=sys.stderr
        )
        return 2
    if live and args.skip_services:
        print(
            "REFUSED: live mode must configure its provider cost ledger through Compose",
            file=sys.stderr,
        )
        return 2
    reserve_tokens = 0
    if live:
        try:
            reserve_tokens = int(
                os.environ.get(
                    "META_REASONING_RESERVE_TOKENS",
                    str(DEFAULT_REASONING_RESERVE_TOKENS),
                )
            )
        except ValueError:
            print(
                "REFUSED: META_REASONING_RESERVE_TOKENS must be a non-negative integer",
                file=sys.stderr,
            )
            return 2
        if reserve_tokens < 0:
            print(
                "REFUSED: META_REASONING_RESERVE_TOKENS must be a non-negative integer",
                file=sys.stderr,
            )
            return 2
    run_id = uuid.uuid4().hex
    args.artifact_dir = args.artifact_dir.resolve()
    args.artifact_dir.mkdir(parents=True, exist_ok=True)
    ledger_path = args.artifact_dir / f"provider-costs-{run_id}.json" if live else None
    guard = CostGuard(
        CAP_USD if live else float("inf"), live, reserve_tokens, ledger_path, args.prior_spend_usd
    )
    if live and args.prior_spend_usd + guard.third_check_reserve >= CAP_USD:
        print("REFUSED: prior spend and third-check reserve exhaust the hard cap", file=sys.stderr)
        return 2
    if live:
        os.environ.update(
            {
                "ICOMIC_E2E_RUN_ID": run_id,
                "ICOMIC_E2E_COST_DIR": str(args.artifact_dir),
                "ICOMIC_E2E_COST_CAP_USD": str(CAP_USD),
                "ICOMIC_E2E_PRIOR_SPEND_USD": str(args.prior_spend_usd),
                "ICOMIC_E2E_THIRD_CHECK_RESERVE_USD": str(round(guard.third_check_reserve, 9)),
            }
        )
    project_id = None
    page_count = 0
    failure = None
    try:
        if not args.skip_services:
            ensure_services()
        guard.refresh()
        page_count, project_id = run_flow(guard, args.artifact_dir)
    except (E2EError, subprocess.CalledProcessError, OSError, ValueError, TimeoutError) as exc:
        failure = type(exc).__name__
        print(f"FAILED: {exc}", file=sys.stderr)
    finally:
        try:
            guard.refresh()
        except E2EError:
            if failure is None:
                failure = "ProviderLedgerUnavailable"
    report = {
        "project_id": project_id or guard.project_id,
        "live": live,
        "status": "failed" if failure else "passed",
        "error_type": failure,
        "failed_stage": guard.current_stage if failure else None,
        "run_id": run_id,
        "hard_cap_usd": CAP_USD if live else None,
        "third_check_reserve_usd": round(guard.third_check_reserve, 9),
        "actual_spend_usd": round(guard.actual, 9),
        "prior_spend_usd": args.prior_spend_usd,
        "cumulative_observed_spend_usd": round(args.prior_spend_usd + guard.actual, 9),
        "provider_cost_report": guard.provider_report,
        "cost_complete": not live
        or (guard.provider_report is not None and guard.provider_report["unpriced_calls"] == 0),
        "pdf_path": str(args.artifact_dir / "latest.pdf") if not failure else None,
        "generated_comic_pages": page_count,
        "stages": guard.stages,
    }
    report_text = json.dumps(report, indent=2) + "\n"
    (args.artifact_dir / f"cost-report-{run_id}.json").write_text(report_text)
    (args.artifact_dir / "cost-report.json").write_text(report_text)
    print(f"Cost report: {args.artifact_dir / 'cost-report.json'}")
    if not failure:
        print(str(args.artifact_dir / "latest.pdf"))
    return 1 if failure else 0


if __name__ == "__main__":
    raise SystemExit(main())
