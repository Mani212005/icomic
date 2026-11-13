#!/usr/bin/env python3
"""Run quality comparison between draft-conditioned and direct-from-brief painted page variants.

Drives a one-page run through the approved draft, then generates the finished painted
page twice from the same approved draft and character reference:
- Variant A: draft-conditioned (composition-conditioned finished paint)
- Variant B: direct-from-brief (composition established directly from the brief)

Outputs saved to artifact directory:
- variant-a-draft-conditioned.webp and variant-a-draft-conditioned.png
- variant-b-direct-from-brief.webp and variant-b-direct-from-brief.png
- approved-draft.webp and approved-draft.png
- reference-character.webp and reference-character.png
- ink-baseline.png
- compare.png (side-by-side contact sheet: baseline, A, B, captain target)
- cost-report.json
"""

from __future__ import annotations

import argparse
import io
import json
import os
import shutil
import sys
import uuid
from pathlib import Path
from typing import Any
from urllib.error import HTTPError, URLError
from urllib.parse import urlsplit, urlunsplit
from urllib.request import Request, urlopen

from PIL import Image, ImageDraw, ImageFont

REPO_ROOT = Path(__file__).resolve().parents[2]
AI_PATH = REPO_ROOT / "ai"
if str(AI_PATH) not in sys.path:
    sys.path.insert(0, str(AI_PATH))

SCRIPTS_E2E_PATH = REPO_ROOT / "scripts" / "e2e"
if str(SCRIPTS_E2E_PATH) not in sys.path:
    sys.path.insert(0, str(SCRIPTS_E2E_PATH))

import comic_e2e
from comic_e2e import (
    CostGuard,
    E2EError,
    checked_call,
    ensure_services,
    json_request,
    live_authorization_error,
    provision_run_user,
    require,
)

DEFAULT_ARTIFACT_DIR = Path("/home/mani/icomic-artifacts/quality-compare")
DEFAULT_INK_BASELINE = Path("/home/mani/icomic-artifacts/quality-before/probe-1-final-page.png")
DEFAULT_TARGET_REF = Path("/home/mani/icomic-artifacts/captain-refs/manhwa-target-quality.png")
SHORT_SCRIPT = (
    "Nia, a young lighthouse keeper, discovers a tiny brass bird during a storm. "
    "The bird lights the dark sea, guiding a lost boat safely home. At sunrise, Nia "
    "sets it free and watches it fly toward the bright horizon."
)


def fetch_image_bytes(image_url: str, storage_url: str | None = None) -> bytes:
    """Fetch image bytes from local storage or remote URL."""
    storage_base = storage_url or comic_e2e.STORAGE_URL
    parsed = urlsplit(image_url)
    if parsed.path.startswith("/storage/fake/"):
        storage = urlsplit(storage_base)
        target_url = urlunsplit((storage.scheme, storage.netloc, parsed.path, parsed.query, ""))
    else:
        target_url = image_url

    req = Request(target_url, headers={"User-Agent": "icomic-quality-compare/1.0"})
    try:
        with urlopen(req, timeout=30) as response:
            content = response.read(25 * 1024 * 1024)
    except (HTTPError, URLError) as exc:
        raise E2EError(f"Failed to fetch image from {target_url}: {exc}") from exc

    if not content:
        raise E2EError(f"Empty image received from {target_url}")
    return content


def save_image_pair(
    raw_bytes: bytes,
    base_name: str,
    artifact_dir: Path,
) -> tuple[Path, Path]:
    """Save original raw bytes (e.g. webp) and lossless PNG copy."""
    artifact_dir.mkdir(parents=True, exist_ok=True)
    webp_path = artifact_dir / f"{base_name}.webp"
    png_path = artifact_dir / f"{base_name}.png"

    webp_path.write_bytes(raw_bytes)
    with Image.open(io.BytesIO(raw_bytes)) as img:
        img.save(png_path, format="PNG")

    return webp_path, png_path


def load_font(size: int) -> ImageFont.ImageFont:
    """Load standard system TTF font or default bitmap font."""
    font_candidates = [
        "/usr/share/fonts/truetype/dejavu/DejaVuSans-Bold.ttf",
        "/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf",
        "/usr/share/fonts/TTF/DejaVuSans-Bold.ttf",
    ]
    for candidate in font_candidates:
        if os.path.exists(candidate):
            try:
                return ImageFont.truetype(candidate, size)
            except OSError:
                pass
    return ImageFont.load_default()


def build_contact_sheet(
    output_path: Path,
    panels_data: list[tuple[str, Path | Image.Image]],
    target_height: int = 800,
) -> Path:
    """Build a side-by-side horizontal contact sheet with labeled headers."""
    header_height = 54
    padding = 16
    border_width = 3

    font = load_font(22)

    loaded_panels: list[tuple[str, Image.Image]] = []
    for label, item in panels_data:
        if isinstance(item, Path):
            if item.exists():
                img = Image.open(item).convert("RGB")
            else:
                img = Image.new("RGB", (600, target_height), color=(40, 40, 40))
                draw = ImageDraw.Draw(img)
                draw.text(
                    (20, target_height // 2),
                    f"Missing:\n{item.name}",
                    fill=(200, 200, 200),
                    font=font,
                )
        else:
            img = item.convert("RGB")

        orig_w, orig_h = img.size
        scaled_w = max(100, round(orig_w * (target_height / orig_h)))
        resample_mode = getattr(getattr(Image, "Resampling", Image), "LANCZOS", 1)
        scaled_img = img.resize((scaled_w, target_height), resample_mode)
        loaded_panels.append((label, scaled_img))

    total_width = sum(p[1].width + 2 * border_width for p in loaded_panels) + padding * (
        len(loaded_panels) + 1
    )
    sheet_height = target_height + header_height + 2 * border_width + 2 * padding

    sheet = Image.new("RGB", (total_width, sheet_height), color=(15, 23, 42))  # Slate 900
    sheet_draw = ImageDraw.Draw(sheet)

    current_x = padding
    for label, panel_img in loaded_panels:
        panel_w = panel_img.width
        box_w = panel_w + 2 * border_width
        box_h = target_height + header_height + 2 * border_width

        # Header background
        sheet_draw.rectangle(
            [current_x, padding, current_x + box_w, padding + header_height],
            fill=(30, 41, 59),  # Slate 800
        )

        # Label text centered
        text_bbox = sheet_draw.textbbox((0, 0), label, font=font)
        text_w = text_bbox[2] - text_bbox[0]
        text_h = text_bbox[3] - text_bbox[1]
        text_x = current_x + (box_w - text_w) // 2
        text_y = padding + (header_height - text_h) // 2
        sheet_draw.text((text_x, text_y), label, fill=(248, 250, 252), font=font)

        # Image border
        sheet_draw.rectangle(
            [
                current_x,
                padding + header_height,
                current_x + box_w,
                padding + box_h,
            ],
            outline=(71, 85, 105),  # Slate 600
            width=border_width,
        )

        # Paste panel image
        sheet.paste(panel_img, (current_x + border_width, padding + header_height + border_width))

        current_x += box_w + padding

    output_path.parent.mkdir(parents=True, exist_ok=True)
    sheet.save(output_path, format="PNG")
    return output_path


def mark_job_done_in_db(job_id: str | None) -> None:
    if not job_id or not comic_e2e.COMPOSE_PROJECT:
        return
    cmd = comic_e2e.compose_command() + [
        "exec",
        "-T",
        "postgres",
        "psql",
        "-U",
        "icomic",
        "-d",
        "icomic",
        "-c",
        f"UPDATE generation_jobs SET status = 'done', completed_at = NOW() WHERE id = '{job_id}'",
    ]
    import subprocess

    subprocess.run(cmd, cwd=REPO_ROOT, check=False, capture_output=True)


def call_painted_page(
    guard: CostGuard,
    chapter_id: str,
    page_id: str,
    variant: str,
) -> str:
    """Generate painted page variant with cost accounting."""
    estimate = guard.before_call("image", 1)
    # The request answers with a receipt; json_request follows the job until it settles.
    resp = json_request(
        f"/api/v1/pages/{page_id}/painted-page",
        "POST",
        {"variant": variant},
    )
    pages = json_request(f"/api/v1/chapters/{chapter_id}/pages").get("pages", [])
    saved_page = next((page for page in pages if page.get("id") == page_id), {})
    if saved_page.get("painted_page_image_job_id") != resp.get("job_id"):
        raise E2EError(f"Variant {variant} job did not produce the page's painted image")
    img_url = require(
        saved_page, "painted_page_image_url", f"Variant {variant} page missing painted image"
    )
    job_id = None
    if isinstance(resp, dict):
        job_record = resp.get("job")
        job_id = resp.get("job_id") or (
            job_record.get("id") if isinstance(job_record, dict) else None
        )
    if job_id:
        mark_job_done_in_db(job_id)
    guard.record("image", job_id, 0.01, estimate)
    return img_url


def run_flow_quality_compare(
    guard: CostGuard,
    artifact_dir: Path,
    ink_baseline_path: Path | None = None,
    target_ref_path: Path | None = None,
    existing_project_id: str | None = None,
) -> dict[str, Any]:
    """Execute flow through approved draft, generate variants A & B, save assets."""
    artifact_dir.mkdir(parents=True, exist_ok=True)

    if existing_project_id:
        project_id = existing_project_id
        guard.project_id = project_id
        print(f"Resuming with existing project: {project_id}")

        chapters = json_request(f"/api/v1/projects/{project_id}/chapters").get("chapters", [])
        if not chapters:
            raise E2EError(f"Existing project {project_id} has no chapters")
        chapter_id = chapters[0]["id"]

        pages = json_request(f"/api/v1/chapters/{chapter_id}/pages").get("pages", [])
        if not pages:
            raise E2EError(f"Existing chapter {chapter_id} has no pages")
        page = pages[0]
        page_id = page["id"]

        character_list = json_request(f"/api/v1/projects/{project_id}/characters").get(
            "characters", []
        )
        nia_char = next((c for c in character_list if c.get("name") == "Nia"), character_list[0])
        char_ref_url = require(nia_char, "reference_image_url", "character reference URL missing")
        draft_url = require(page, "draft_image_url", "draft image URL missing")

        var_a_url = page.get("painted_page_image_url")
        if var_a_url and page.get("painted_page_variant") == "draft-conditioned":
            print(f"PASS Variant A already generated: {var_a_url}")
        else:
            print("Generating Variant A (draft-conditioned)...", flush=True)
            var_a_url = call_painted_page(guard, chapter_id, page_id, "draft-conditioned")
            print(f"PASS Variant A generated: {var_a_url}")
    else:
        # 1. Project creation
        guard.current_stage = "project"
        project_response = json_request(
            "/api/v1/projects",
            "POST",
            {
                "title": "Lighthouse Bird",
                "original_script": SHORT_SCRIPT,
                "art_style": "storybook",
                "chapter_count": 1,
            },
        )
        project = require(project_response, "project", "project creation response has no project")
        project_id = require(project, "id", "created project has no id")
        guard.project_id = project_id
        print(f"PASS project created: {project_id}")

        # 2. Script analysis
        analysis = checked_call(
            guard,
            "analysis",
            1,
            "POST",
            f"/api/v1/projects/{project_id}/analyze",
        )
        characters = analysis.get("character_profiles", [])
        if not characters:
            raise E2EError("script analysis returned no characters")
        print(f"PASS script analyzed: {len(characters)} characters")

        # 3. Character references & approval
        character_list = json_request(f"/api/v1/projects/{project_id}/characters").get(
            "characters", []
        )
        if len(character_list) != len(characters):
            raise E2EError("character list does not match script analysis")
        for character in character_list:
            cid = require(character, "id", "character has no id")
            if character.get("visual_attributes", {}).get("reference_role") != "extra":
                checked_call(guard, "image", 1, "POST", f"/api/v1/characters/{cid}/reference-image")
            json_request(f"/api/v1/characters/{cid}/approve", "POST")
        print("PASS character references generated and approved")

        # Fetch approved Nia character reference URL
        character_list = json_request(f"/api/v1/projects/{project_id}/characters").get(
            "characters", []
        )
        nia_char = next((c for c in character_list if c.get("name") == "Nia"), character_list[0])
        char_ref_url = nia_char.get("reference_image_url")
        if not char_ref_url:
            raise E2EError("approved character reference URL is missing")

        # 4. Chapters & outline
        outline = checked_call(
            guard, "outline", 1, "POST", f"/api/v1/projects/{project_id}/chapters"
        )
        chapters = outline.get("chapters", [])
        if not chapters:
            raise E2EError("chapter generation returned no chapters")
        chapter_id = chapters[0]["id"]
        json_request(f"/api/v1/chapters/{chapter_id}/approve", "POST")
        print("PASS chapter outline generated and approved")

        # 5. Panels & pages
        panel_response = checked_call(
            guard, "panels", 1, "POST", f"/api/v1/chapters/{chapter_id}/panels"
        )
        pages = panel_response.get("pages", [])
        panels = panel_response.get("panels", [])
        if not pages or not panels:
            raise E2EError("panel generation produced no pages or panels")
        page_id = pages[0]["id"]
        guard.panel_count = len(panels)
        print(f"PASS panels and page generated: {len(panels)} panels on page {page_id}")

        # 6. Draft sketch generation & approval
        checked_call(guard, "image", 1, "POST", f"/api/v1/pages/{page_id}/draft")
        json_request(f"/api/v1/pages/{page_id}/draft/approve", "POST")
        print("PASS draft sketch generated and approved")

        # Fetch approved draft URL
        page_list = json_request(f"/api/v1/chapters/{chapter_id}/pages").get("pages", [])
        cur_page = next((p for p in page_list if p.get("id") == page_id), page_list[0])
        draft_url = cur_page.get("draft_image_url")
        if not draft_url:
            raise E2EError("approved draft image URL is missing")

        # 7. Generate finished painted page: Variant A (draft-conditioned)
        print("Generating Variant A (draft-conditioned)...", flush=True)
        var_a_url = call_painted_page(guard, chapter_id, page_id, "draft-conditioned")
        print(f"PASS Variant A generated: {var_a_url}")

    # Ensure draft approval is fresh before Variant B
    json_request(f"/api/v1/pages/{page_id}/draft/approve", "POST")

    # 8. Generate finished painted page: Variant B (direct-from-brief)
    print("Generating Variant B (direct-from-brief)...", flush=True)
    var_b_url = call_painted_page(guard, chapter_id, page_id, "direct-from-brief")
    print(f"PASS Variant B generated: {var_b_url}")

    # 9. Fetch and save all assets
    print("Fetching and saving image assets...", flush=True)
    var_a_bytes = fetch_image_bytes(var_a_url)
    save_image_pair(var_a_bytes, "variant-a-draft-conditioned", artifact_dir)

    var_b_bytes = fetch_image_bytes(var_b_url)
    save_image_pair(var_b_bytes, "variant-b-direct-from-brief", artifact_dir)

    draft_bytes = fetch_image_bytes(draft_url)
    save_image_pair(draft_bytes, "approved-draft", artifact_dir)

    char_bytes = fetch_image_bytes(char_ref_url)
    save_image_pair(char_bytes, "reference-character", artifact_dir)

    # Ink baseline
    ink_dest = artifact_dir / "ink-baseline.png"
    if ink_baseline_path and ink_baseline_path.exists():
        shutil.copyfile(ink_baseline_path, ink_dest)
        print(f"PASS ink baseline copied from {ink_baseline_path}")
    elif not ink_dest.exists():
        # Create a stub if not provided
        Image.new("RGB", (1920, 1280), color=(240, 240, 240)).save(ink_dest, format="PNG")

    # 10. Build contact sheet compare.png
    print("Building contact sheet compare.png...", flush=True)
    target_path = (
        target_ref_path if (target_ref_path and target_ref_path.exists()) else DEFAULT_TARGET_REF
    )
    panels_for_sheet: list[tuple[str, Path | Image.Image]] = [
        ("Ink Baseline (Probe 1 Line Art + Flat Fill)", ink_dest),
        ("Variant A (Draft-Conditioned)", artifact_dir / "variant-a-draft-conditioned.png"),
        ("Variant B (Direct-From-Brief)", artifact_dir / "variant-b-direct-from-brief.png"),
        ("Target Reference (Manhwa Quality Reference)", target_path),
    ]
    contact_sheet_path = artifact_dir / "compare.png"
    build_contact_sheet(contact_sheet_path, panels_for_sheet, target_height=800)
    print(f"PASS contact sheet saved: {contact_sheet_path}")

    return {
        "project_id": project_id,
        "page_id": page_id,
        "chapter_id": chapter_id,
        "variant_a_url": var_a_url,
        "variant_b_url": var_b_url,
        "draft_url": draft_url,
        "character_ref_url": char_ref_url,
    }


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
        default=2.0,
        help="required total hard cap for live mode (greater than 0, at most 2 USD)",
    )
    parser.add_argument(
        "--skip-services",
        action="store_true",
        help="use already-running local services or external mocks",
    )
    parser.add_argument(
        "--project-id",
        type=str,
        default=None,
        help="reuse already provisioned project in running service",
    )
    parser.add_argument(
        "--run-id",
        type=str,
        default=None,
        help="explicit run ID for ledger tracking",
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
        default=DEFAULT_ARTIFACT_DIR,
        help="directory for quality comparison outputs and cost report",
    )
    parser.add_argument(
        "--ink-baseline",
        type=Path,
        default=DEFAULT_INK_BASELINE,
        help="path to probe 1 baseline page PNG",
    )
    parser.add_argument(
        "--target-ref",
        type=Path,
        default=DEFAULT_TARGET_REF,
        help="path to captain's manhwa target reference PNG",
    )
    parser.add_argument(
        "--api-url",
        type=str,
        default=None,
        help="override API URL (default from comic_e2e or Compose)",
    )
    parser.add_argument(
        "--storage-url",
        type=str,
        default=None,
        help="override storage URL (default from comic_e2e or Compose)",
    )
    return parser.parse_args(argv)


def main(argv: list[str] | None = None) -> int:
    args = parse_args(argv)
    live = args.live

    if live:
        auth_error = live_authorization_error(True, os.environ, args.cost_cap_usd)
        if auth_error is not None:
            print(f"REFUSED: {auth_error}", file=sys.stderr)
            return 2

    run_id = args.run_id or uuid.uuid4().hex
    quota_policy = None

    comic_e2e.COMPOSE_PROJECT = f"icomic-e2e-{run_id}"
    if not args.skip_services:
        daily = 100
        monthly = 1000
        quota_policy = {
            "scope": "isolated_e2e_stack",
            "daily_image_limit": daily,
            "monthly_image_limit": monthly,
            "compose_project": comic_e2e.COMPOSE_PROJECT,
        }
        os.environ.update(
            {
                "ICOMIC_E2E_DAILY_IMAGES": str(daily),
                "ICOMIC_E2E_MONTHLY_IMAGES": str(monthly),
                "ICOMIC_E2E_ISOLATED_STACK": "1",
            }
        )

    args.artifact_dir = args.artifact_dir.resolve()
    args.artifact_dir.mkdir(parents=True, exist_ok=True)

    diagnostics_dir = args.artifact_dir / f"diagnostics-{run_id}"
    if live:
        diagnostics_dir.mkdir(mode=0o700, exist_ok=True)

    ledger_path = args.artifact_dir / f"provider-costs-{run_id}.json" if live else None
    reserve_tokens = 1024

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

    if args.api_url:
        comic_e2e.API_URL = args.api_url.rstrip("/")
    if args.storage_url:
        comic_e2e.STORAGE_URL = args.storage_url.rstrip("/")

    failure = None
    flow_data = {}

    try:
        if not args.skip_services:
            ensure_services()
        else:
            if not comic_e2e.RUN_USER_ID:
                comic_e2e.RUN_USER_ID = os.environ.get("RUN_USER_ID") or str(uuid.uuid4())
                guard.user_id = comic_e2e.RUN_USER_ID

        if not args.skip_services:
            guard.current_stage = "provision_user"
            comic_e2e.RUN_USER_ID = provision_run_user()
            guard.user_id = comic_e2e.RUN_USER_ID
            print(f"PASS isolated run user created: {comic_e2e.RUN_USER_ID}")

        guard.refresh()
        flow_data = run_flow_quality_compare(
            guard=guard,
            artifact_dir=args.artifact_dir,
            ink_baseline_path=args.ink_baseline,
            target_ref_path=args.target_ref,
            existing_project_id=args.project_id,
        )
    except Exception as exc:  # noqa: BLE001
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
        "api_url": comic_e2e.API_URL,
        "storage_url": comic_e2e.STORAGE_URL,
        "user_id": guard.user_id,
        "project_id": flow_data.get("project_id") or guard.project_id,
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
        or (
            guard.provider_report is not None
            and guard.provider_report.get("unpriced_calls", 0) == 0
        ),
        "diagnostics_dir": str(diagnostics_dir) if live else None,
        "diagnostic_files": [str(p) for p in sorted(diagnostics_dir.glob("response-*.json"))]
        if live
        else [],
        "flow_data": flow_data,
        "stages": guard.stages,
    }

    report_text = json.dumps(report, indent=2) + "\n"
    (args.artifact_dir / f"cost-report-{run_id}.json").write_text(report_text)
    (args.artifact_dir / "cost-report.json").write_text(report_text)
    print(f"Cost report: {args.artifact_dir / 'cost-report.json'}")

    return 1 if failure else 0


if __name__ == "__main__":
    raise SystemExit(main())
