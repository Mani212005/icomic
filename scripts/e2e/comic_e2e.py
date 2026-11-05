#!/usr/bin/env python3
"""Run the full comic flow against the local Go API and configured AI adapters.

Usage: make e2e-comic [E2E_ARGS="--live --cost-cap-usd 2"]
The default adapter configuration is fake. Live use requires --live, ICOMIC_E2E_LIVE=1,
and an explicit --cost-cap-usd (at most 2); all adapter selection still comes from ADAPTER_* environment.
Each managed run starts a unique isolated Compose stack and provisions its own
PostgreSQL user. E2E image limits default to 100/day and 1000/month in that stack
only; --skip-services leaves external service policy unchanged. No existing
user's quota counters or persisted policy rows are changed.
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
from urllib.parse import urlsplit, urlunsplit
from urllib.request import Request, urlopen

REPO_ROOT = Path(__file__).resolve().parents[2]
ARTIFACT_DIR = Path("/home/mani/icomic-artifacts/comic-e2e")
CAP_USD = 2.0
RUN_USER_ID: str | None = None
COMPOSE_PROJECT: str | None = None
API_URL = os.environ.get("ICOMIC_E2E_API_URL", "http://127.0.0.1:8080").rstrip("/")
STORAGE_URL = os.environ.get("ICOMIC_E2E_STORAGE_URL", "http://127.0.0.1:8000").rstrip("/")

AI_PATH = REPO_ROOT / "ai"
sys.path.insert(0, str(AI_PATH))
from app.services.pacing import chapter_count, panel_count, split_text

sys.path.insert(0, str(REPO_ROOT / "scripts/e2e"))
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
    if cost_cap is None or not math.isfinite(cost_cap) or not 0 < cost_cap <= CAP_USD:
        return "live mode requires --cost-cap-usd greater than 0 and at most 2"
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
        alert_usd: float = 1.0,
    ):
        self.cap = cap
        self.live = live
        self.actual = 0.0
        self.stages: list[dict[str, Any]] = []
        self.ledger_path = ledger_path
        self.prior_spend = prior_spend
        self.alert_usd = alert_usd
        self.alert_raised = False
        self.provider_report: dict[str, Any] | None = None
        self.current_stage: str | None = None
        self.project_id: str | None = None
        self.user_id: str | None = None
        self.script_file: Path | None = None
        self.panel_count = 0
        self.pdf_page_count = 0
        self.reserve_tokens = reserve_tokens if live else 0
        self.third_check_reserve = (
            calculate_checklist_worst_case(self.reserve_tokens) if live else 0.0
        )

    def before_call(self, stage: str, count: int = 1) -> float:
        self.current_stage = stage
        self.refresh()
        estimate = 0.0
        if self.live:
            per_call = (
                estimate_call_worst_case("analysis", self.reserve_tokens)
                if stage == "language"
                else estimate_call_worst_case(stage, self.reserve_tokens)
            )
            estimate = per_call * count
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
            self.check_alert()
        except (KeyError, TypeError, ValueError, OSError) as exc:
            raise E2EError("Provider cost ledger is invalid; refusing further calls") from exc

    def check_alert(self) -> None:
        if not self.alert_raised and self.prior_spend + self.actual >= self.alert_usd:
            self.alert_raised = True
            print(
                f"COST ALERT: cumulative observed spend ${self.prior_spend + self.actual:.9f} "
                f"USD reached ${self.alert_usd:.9f} USD; continuing within the hard cap",
                flush=True,
            )

    def record(self, stage: str, job_id: str | None, usage_cost: float, estimate: float) -> None:
        if self.ledger_path is not None:
            self.refresh()
            print(
                f"Cumulative observed spend: ${self.prior_spend + self.actual:.9f} USD after {stage}"
            )
            return
        amount = max(0.0, usage_cost)
        if (
            self.live
            and stage in {"language", "vision", "analysis", "outline", "panels", "palette", "image"}
            and amount <= 0
        ):
            raise E2EError(f"live {stage} call returned no billable usage record; aborting")
        self.actual += amount
        self.check_alert()
        if self.prior_spend + self.actual + self.third_check_reserve > self.cap + 1e-12:
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
    headers = {"Content-Type": "application/json"}
    if RUN_USER_ID is not None:
        headers["X-User-ID"] = RUN_USER_ID
    request = Request(
        f"{API_URL}{path}",
        data=data,
        method=method,
        headers=headers,
    )
    try:
        with urlopen(request, timeout=16 * 60) as response:
            body = response.read()
            if "application/pdf" in response.headers.get("Content-Type", ""):
                return body
            return json.loads(body) if body else {}
    except HTTPError as exc:
        detail = exc.read().decode("utf-8", errors="replace")
        raise E2EError(f"{method} {path} returned HTTP {exc.code}: {detail}") from exc
    except URLError as exc:
        raise E2EError(f"{method} {path} failed: {exc.reason}") from exc


def binary_request(path: str, method: str, body: bytes, content_type: str) -> Any:
    headers = {"Content-Type": content_type}
    if RUN_USER_ID is not None:
        headers["X-User-ID"] = RUN_USER_ID
    request = Request(f"{API_URL}{path}", data=body, method=method, headers=headers)
    try:
        with urlopen(request, timeout=60) as response:
            return json.loads(response.read())
    except HTTPError as exc:
        detail = exc.read().decode("utf-8", errors="replace")
        raise E2EError(f"{method} {path} returned HTTP {exc.code}: {detail}") from exc
    except URLError as exc:
        raise E2EError(f"{method} {path} failed: {exc.reason}") from exc


def fetch_line_art_image(image_url: str) -> bytes:
    parsed = urlsplit(image_url)
    if parsed.scheme != "http" or not parsed.path.startswith("/storage/fake/"):
        raise E2EError("fake E2E line art URL is outside local fake storage")
    storage = urlsplit(STORAGE_URL)
    local_url = urlunsplit((storage.scheme, storage.netloc, parsed.path, "", ""))
    try:
        with urlopen(local_url, timeout=15) as response:
            image_bytes = response.read(10 * 1024 * 1024 + 1)
    except (HTTPError, URLError) as exc:
        raise E2EError(f"could not fetch fake line art image: {exc}") from exc
    if not image_bytes or len(image_bytes) > 10 * 1024 * 1024:
        raise E2EError("fake line art image is empty or exceeds 10 MiB")
    return image_bytes


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


def compose_command() -> list[str]:
    command = ["docker", "compose", "-f", "deploy/docker-compose.yml"]
    if COMPOSE_PROJECT is not None:
        command += ["-f", "deploy/docker-compose.e2e.yml", "-p", COMPOSE_PROJECT]
    return command


def ensure_services() -> None:
    global API_URL, STORAGE_URL
    if COMPOSE_PROJECT is None:
        raise E2EError("managed services require a unique isolated E2E project")
    command = compose_command() + [
        "up",
        "-d",
        "--build",
        "postgres",
        "redis",
        "ai",
        "backend",
    ]
    subprocess.run(command, cwd=REPO_ROOT, check=True)
    addresses = []
    for service, port in (("backend", "8080"), ("ai", "8000")):
        output = subprocess.run(
            compose_command() + ["port", service, port],
            cwd=REPO_ROOT,
            capture_output=True,
            text=True,
            check=True,
        ).stdout.strip()
        host, _, published = output.rpartition(":")
        if host != "127.0.0.1" or not published.isdigit():
            raise E2EError("isolated E2E service did not publish a loopback port")
        addresses.append("http://" + output)
    API_URL, STORAGE_URL = addresses
    print(f"Isolated E2E endpoints: API={API_URL} storage={STORAGE_URL}", flush=True)
    wait_for_api()


def require(mapping: dict[str, Any], key: str, message: str) -> Any:
    value = mapping.get(key)
    if value is None or value == "":
        raise E2EError(message)
    return value


def verify_pdf(pdf: bytes, expected_comic_pages: int) -> int:
    if not pdf or not pdf.startswith(b"%PDF-"):
        raise E2EError("download is empty or missing the PDF header")
    page_count = len(re.findall(rb"/Type\s*/Page\b", pdf))
    if page_count != expected_comic_pages + 1:
        raise E2EError(
            f"PDF contains {page_count} pages; expected {expected_comic_pages} comic pages plus one cover"
        )

    return page_count


def provision_run_user() -> str:
    """Create an isolated fixture without changing existing users or quota policy."""
    user_id = str(uuid.uuid4())
    command = compose_command() + [
        "exec",
        "-T",
        "postgres",
        "psql",
        "-U",
        "icomic",
        "-d",
        "icomic",
        "--single-transaction",
        "--file=-",
        "-v",
        "ON_ERROR_STOP=1",
        "--set",
        f"e2e_user_id={user_id}",
        "--set",
        f"e2e_email=comic-e2e-{uuid.uuid4().hex}@example.invalid",
        "--set",
        f"e2e_credit_id={uuid.uuid4()}",
    ]
    # psql's quoted variables bind literals safely. A new user and opening credit
    # commit together; no upsert, cleanup, quota reset or policy override occurs.
    sql = """
INSERT INTO users (id, email, password_hash, plan_id)
VALUES (:'e2e_user_id'::uuid, :'e2e_email', '!', 'free');
INSERT INTO credit_ledger
    (id, user_id, project_id, generation_job_id, transaction_type, amount, balance_after)
VALUES (:'e2e_credit_id'::uuid, :'e2e_user_id'::uuid, NULL, NULL, 'adjustment', 100, 100);
"""
    subprocess.run(command, cwd=REPO_ROOT, input=sql, text=True, capture_output=True, check=True)
    return user_id


def run_flow(
    guard: CostGuard,
    artifact_dir: Path = ARTIFACT_DIR,
    finish_route: str | None = None,
) -> tuple[int, str]:
    from colouring import colour_enclosed_regions

    if finish_route is None:
        finish_route = getattr(guard, "finish_route", "painted")

    global RUN_USER_ID
    guard.current_stage = "provision_user"
    RUN_USER_ID = provision_run_user()
    guard.user_id = RUN_USER_ID
    print(f"PASS isolated run user created: {RUN_USER_ID}")
    usage = json_request("/api/v1/me/credit-usage")
    if float(usage.get("balance", 0)) < 10:
        raise E2EError("isolated run user has insufficient starting credits")

    script = (
        "Nia, a young lighthouse keeper, discovers a tiny brass bird during a storm. "
        "The bird lights the dark sea, guiding a lost boat safely home. At sunrise, Nia "
        "sets it free and watches it fly toward the bright horizon."
    )
    if guard.script_file is not None:
        script = guard.script_file.read_text()
    expected_panels = panel_count(len(script.split()))
    expected_chapters = chapter_count(len(script.split()))
    project_response = json_request(
        "/api/v1/projects",
        "POST",
        {
            "title": "Lighthouse Bird",
            "original_script": script,
            "art_style": "storybook",
            "chapter_count": expected_chapters,
        },
    )
    project = require(project_response, "project", "project creation response has no project")
    project_id = require(project, "id", "created project has no id")
    guard.project_id = project_id
    print(f"PASS project created: {project_id}")

    analysis = checked_call(
        guard,
        "analysis",
        math.ceil(len(script.split()) / 500),
        "POST",
        f"/api/v1/projects/{project_id}/analyze",
    )
    characters = analysis.get("character_profiles", [])
    if not characters:
        raise E2EError("script analysis returned no characters")
    print(f"PASS script written and analyzed: {len(characters)} characters")

    character_list = json_request(f"/api/v1/projects/{project_id}/characters").get("characters", [])
    if len(character_list) != len(characters):
        raise E2EError("character list does not match script analysis")
    for character in character_list:
        cid = require(character, "id", "generated character has no id")
        if character.get("visual_attributes", {}).get("reference_role") != "extra":
            checked_call(guard, "image", 1, "POST", f"/api/v1/characters/{cid}/reference-image")
        json_request(f"/api/v1/characters/{cid}/approve", "POST")
    main_count = sum(
        c.get("visual_attributes", {}).get("reference_role") != "extra" for c in character_list
    )
    print(
        f"PASS character references generated and approved: {main_count}; all {len(character_list)} profiles approved"
    )

    outline_calls = sum(
        max(1, math.ceil(len(part.split()) / 500)) for part in split_text(script, expected_chapters)
    )
    outline = checked_call(
        guard, "outline", outline_calls, "POST", f"/api/v1/projects/{project_id}/chapters"
    )
    chapters = outline.get("chapters", [])
    if not chapters:
        raise E2EError("chapter generation returned no chapters")
    for chapter in chapters:
        json_request(f"/api/v1/chapters/{chapter['id']}/approve", "POST")
    print(f"PASS chapter outline generated and approved: {len(chapters)}")

    panels = []
    pages = []
    page_chapter_ids = {}
    expected_pages = 0
    for index, chapter in enumerate(chapters):
        chapter_panels = (
            index + 1
        ) * expected_panels // expected_chapters - index * expected_panels // expected_chapters
        chapter_pages = math.ceil(chapter_panels / 5)
        expected_pages += chapter_pages
        panel_response = checked_call(
            guard, "panels", chapter_pages, "POST", f"/api/v1/chapters/{chapter['id']}/panels"
        )
        generated_panels = panel_response.get("panels", [])
        generated_pages = panel_response.get("pages", [])
        if len(generated_panels) != chapter_panels or len(generated_pages) != chapter_pages:
            raise E2EError("chapter panel/page count differs from the complete script plan")
        listed_panels = json_request(f"/api/v1/chapters/{chapter['id']}/panels").get("panels", [])
        listed_pages = json_request(f"/api/v1/chapters/{chapter['id']}/pages").get("pages", [])
        if len(listed_panels) != chapter_panels or len(listed_pages) != chapter_pages:
            raise E2EError("persisted panel/page count differs from generation")
        if sorted(pid for page in generated_pages for pid in page["panel_ids"]) != sorted(
            p["id"] for p in generated_panels
        ):
            raise E2EError("pages dropped or duplicated panels")
        panels.extend(generated_panels)
        pages.extend(generated_pages)
        page_chapter_ids.update({page["id"]: chapter["id"] for page in generated_pages})
    if (
        len(chapters) != expected_chapters
        or len(panels) != expected_panels
        or len(pages) != expected_pages
    ):
        raise E2EError("comic panel/page count differs from the complete script plan")
    if 3000 <= len(script.split()) <= 6000 and not (
        50 <= len(panels) <= 60 and 10 <= len(pages) <= 14
    ):
        raise E2EError("decent-size script did not produce 50-60 panels across 10-14 pages")
    guard.panel_count = len(panels)
    print(f"PASS comic pages generated: {len(panels)} panels across {len(pages)} pages")

    for index, page in enumerate(pages):
        page_id = page["id"]
        checked_call(guard, "image", 1, "POST", f"/api/v1/pages/{page_id}/draft")
        json_request(f"/api/v1/pages/{page_id}/draft/approve", "POST")

        page_route = finish_route
        if finish_route == "both":
            page_route = "painted" if index % 2 == 0 else "ink"

        if page_route == "painted":
            checked_call(guard, "image", 1, "POST", f"/api/v1/pages/{page_id}/line-art")
            json_request(f"/api/v1/pages/{page_id}/line-art/approve", "POST")
            page_chapter_id = page_chapter_ids[page_id]
            current_pages = json_request(f"/api/v1/chapters/{page_chapter_id}/pages").get(
                "pages", []
            )
            current_page = next((item for item in current_pages if item.get("id") == page_id), None)
            require(
                current_page or {},
                "painted_page_image_url",
                "approved painted page URL missing",
            )
        else:
            checked_call(
                guard,
                "image",
                1,
                "POST",
                f"/api/v1/pages/{page_id}/line-art",
                payload={"variant": "ink"},
            )
            json_request(f"/api/v1/pages/{page_id}/line-art/approve?variant=ink", "POST")
            palette_result = checked_call(
                guard,
                "palette",
                1,
                "POST",
                f"/api/v1/pages/{page_id}/colour-palette/suggest",
            )
            page_chapter_id = page_chapter_ids[page_id]
            current_pages = json_request(f"/api/v1/chapters/{page_chapter_id}/pages").get(
                "pages", []
            )
            current_page = next((item for item in current_pages if item.get("id") == page_id), None)
            line_art_url = require(
                current_page or {}, "line_art_image_url", "approved line art URL missing"
            )
            line_art_bytes = fetch_line_art_image(line_art_url)
            coloured_page = colour_enclosed_regions(
                line_art_bytes,
                require(palette_result, "palette", "palette missing"),
            )
            binary_request(
                f"/api/v1/pages/{page_id}/colour-image", "PUT", coloured_page, "image/png"
            )
            json_request(f"/api/v1/pages/{page_id}/colour/approve", "POST")

    if finish_route == "painted":
        print(f"PASS pages painted and approved: {len(pages)}")
    elif finish_route == "ink":
        print(f"PASS pages inked and coloured: {len(pages)}")
    else:
        print(f"PASS pages finished with both painted and ink routes: {len(pages)}")

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
    guard.pdf_page_count = verify_pdf(pdf, len(pages))
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
        "--script-file", type=Path, help="complete story source; defaults to the short smoke script"
    )
    parser.add_argument(
        "--live",
        action="store_true",
        help="explicitly authorize configured real adapters",
    )
    parser.add_argument(
        "--cost-cap-usd",
        type=float,
        help="required total hard cap for live mode (greater than 0, at most 2 USD)",
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
        "--alert-usd",
        type=float,
        default=1.0,
        help="log once when cumulative observed spend reaches this USD threshold (default 1)",
    )
    parser.add_argument(
        "--prior-spend-source",
        default=None,
        help="source of the explicitly supplied recorded prior spend, saved in the cost report",
    )
    parser.add_argument(
        "--artifact-dir",
        type=Path,
        default=ARTIFACT_DIR,
        help="directory for the verified PDF and cost report",
    )
    parser.add_argument(
        "--finish-route",
        choices=["painted", "ink", "both"],
        default="painted",
        help="finish stage route: painted (default), ink (line art + palette + colours), or both",
    )
    return parser.parse_args(argv)


def main(argv: list[str] | None = None) -> int:
    global RUN_USER_ID, COMPOSE_PROJECT
    RUN_USER_ID = None
    COMPOSE_PROJECT = None
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
    if not math.isfinite(args.alert_usd) or args.alert_usd <= 0:
        print("REFUSED: --alert-usd must be finite and positive", file=sys.stderr)
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
    quota_policy = {"scope": "external_services", "limits": "unchanged"}
    if not args.skip_services:
        try:
            daily = int(os.environ.get("ICOMIC_E2E_DAILY_IMAGES", "100"))
            monthly = int(os.environ.get("ICOMIC_E2E_MONTHLY_IMAGES", "1000"))
        except ValueError:
            print("REFUSED: E2E image limits must be integers", file=sys.stderr)
            return 2
        if daily <= 0 or monthly < daily:
            print("REFUSED: E2E limits require daily > 0 and monthly >= daily", file=sys.stderr)
            return 2
        COMPOSE_PROJECT = f"icomic-e2e-{run_id}"
        quota_policy = {
            "scope": "isolated_e2e_stack",
            "daily_image_limit": daily,
            "monthly_image_limit": monthly,
            "compose_project": COMPOSE_PROJECT,
        }
        print(
            f"E2E quota configuration: daily={daily} monthly={monthly} project={COMPOSE_PROJECT}; persisted policy and existing counters unchanged",
            flush=True,
        )
    args.artifact_dir = args.artifact_dir.resolve()
    if args.artifact_dir == REPO_ROOT:
        print(
            "REFUSED: use a dedicated artifact directory, not the repository root", file=sys.stderr
        )
        return 2
    args.artifact_dir.mkdir(parents=True, exist_ok=True)
    if args.artifact_dir.is_relative_to(REPO_ROOT):
        # Provider outputs are local diagnostic artifacts, never source files.
        ignore = args.artifact_dir / ".gitignore"
        existing = ignore.read_text() if ignore.exists() else ""
        ignore.write_text(existing + ("\n" if existing else "") + "*\n")
    diagnostics_dir = args.artifact_dir / f"diagnostics-{run_id}"
    if live:
        # Create on the host so container-written evidence can retain this owner.
        diagnostics_dir.mkdir(mode=0o700)
    ledger_path = args.artifact_dir / f"provider-costs-{run_id}.json" if live else None
    guard = CostGuard(
        args.cost_cap_usd if live else float("inf"),
        live,
        reserve_tokens,
        ledger_path,
        args.prior_spend_usd,
        args.alert_usd,
    )
    if live and args.prior_spend_usd + guard.third_check_reserve >= args.cost_cap_usd:
        print("REFUSED: prior spend and third-check reserve exhaust the hard cap", file=sys.stderr)
        return 2
    if live:
        os.environ.update(
            {
                "ICOMIC_E2E_RUN_ID": run_id,
                "ICOMIC_E2E_DIAGNOSTICS_DIR": f"/var/lib/icomic-e2e/diagnostics-{run_id}",
                "ICOMIC_E2E_COST_DIR": str(args.artifact_dir),
                "ICOMIC_E2E_COST_CAP_USD": str(args.cost_cap_usd),
                "ICOMIC_E2E_PRIOR_SPEND_USD": str(args.prior_spend_usd),
                "ICOMIC_E2E_THIRD_CHECK_RESERVE_USD": str(round(guard.third_check_reserve, 9)),
            }
        )
    guard.script_file = args.script_file
    guard.finish_route = args.finish_route
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
        "quota_policy": quota_policy,
        "api_url": API_URL,
        "storage_url": STORAGE_URL,
        "user_id": guard.user_id,
        "project_id": project_id or guard.project_id,
        "live": live,
        "status": "failed" if failure else "passed",
        "error_type": failure,
        "failed_stage": guard.current_stage if failure else None,
        "run_id": run_id,
        "hard_cap_usd": args.cost_cap_usd if live else None,
        "alert_threshold_usd": args.alert_usd,
        "alert_raised": guard.alert_raised,
        "prior_spend_source": args.prior_spend_source,
        "third_check_reserve_usd": round(guard.third_check_reserve, 9),
        "actual_spend_usd": round(guard.actual, 9),
        "prior_spend_usd": args.prior_spend_usd,
        "cumulative_observed_spend_usd": round(args.prior_spend_usd + guard.actual, 9),
        "provider_cost_report": guard.provider_report,
        "cost_complete": not live
        or (guard.provider_report is not None and guard.provider_report["unpriced_calls"] == 0),
        "diagnostics_dir": str(diagnostics_dir) if live else None,
        "diagnostic_files": [str(p) for p in sorted(diagnostics_dir.glob("response-*.json"))],
        "pdf_path": str(args.artifact_dir / "latest.pdf") if not failure else None,
        "generated_comic_pages": page_count,
        "generated_panels": guard.panel_count,
        "pdf_pages": guard.pdf_page_count,
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
