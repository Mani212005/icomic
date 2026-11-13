"""Hermetic unit tests for quality comparison runner and call plan."""

from __future__ import annotations

import io
import sys
from pathlib import Path
from typing import Any
from unittest.mock import patch

import pytest
from PIL import Image

REPO_ROOT = Path(__file__).resolve().parents[3]
AI_PATH = REPO_ROOT / "ai"
SCRIPTS_PATH = REPO_ROOT / "scripts" / "manual"
SCRIPTS_E2E_PATH = REPO_ROOT / "scripts" / "e2e"

for path in (str(AI_PATH), str(SCRIPTS_PATH), str(SCRIPTS_E2E_PATH)):
    if path not in sys.path:
        sys.path.insert(0, path)

import comic_e2e
from comic_e2e import CostGuard, E2EError, live_authorization_error
from quality_compare import (
    build_contact_sheet,
    main,
    run_flow_quality_compare,
    save_image_pair,
)


def _create_dummy_image_bytes(format: str = "PNG") -> bytes:
    buf = io.BytesIO()
    img = Image.new("RGB", (64, 64), color=(100, 150, 200))
    img.save(buf, format=format)
    return buf.getvalue()


def test_live_authorization_error_guard():
    # In CI / test environment, live mode is strictly rejected
    assert live_authorization_error(True, {"CI": "true", "ICOMIC_E2E_LIVE": "1"}, 2.0) is not None
    assert (
        live_authorization_error(True, {"PYTEST_CURRENT_TEST": "1", "ICOMIC_E2E_LIVE": "1"}, 2.0)
        is not None
    )

    # Missing --live flag
    assert (
        live_authorization_error(False, {"ICOMIC_E2E_LIVE": "1"}, 2.0)
        == "live mode requires the --live flag"
    )

    # Missing env flag
    assert live_authorization_error(True, {}, 2.0) == "live mode requires ICOMIC_E2E_LIVE=1"

    # Cost cap bounds: at most 2.0
    assert (
        live_authorization_error(True, {"ICOMIC_E2E_LIVE": "1"}, 2.01)
        == "live mode requires --cost-cap-usd greater than 0 and at most 2"
    )
    assert (
        live_authorization_error(True, {"ICOMIC_E2E_LIVE": "1"}, 0.0)
        == "live mode requires --cost-cap-usd greater than 0 and at most 2"
    )

    # Valid authorization outside CI/tests
    assert live_authorization_error(True, {"ICOMIC_E2E_LIVE": "1"}, 2.0) is None


def test_cost_guard_refuses_when_budget_exhausted():
    guard = CostGuard(cap=1.0, live=True, prior_spend=0.99)
    with pytest.raises(E2EError, match="cost guard refused"):
        guard.before_call("image", 1)


def test_save_image_pair_and_contact_sheet(tmp_path: Path):
    dummy_webp = _create_dummy_image_bytes("WEBP")
    webp_path, png_path = save_image_pair(dummy_webp, "test-asset", tmp_path)

    assert webp_path.is_file()
    assert png_path.is_file()
    with Image.open(png_path) as im:
        assert im.format == "PNG"

    contact_sheet = tmp_path / "contact.png"
    build_contact_sheet(
        contact_sheet,
        [
            ("Panel A", png_path),
            ("Panel B", png_path),
        ],
        target_height=200,
    )
    assert contact_sheet.is_file()
    with Image.open(contact_sheet) as sheet:
        assert sheet.format == "PNG"
        assert sheet.height > 200


def test_quality_compare_call_plan_hermetic(tmp_path: Path):
    recorded_calls: list[tuple[str, str, Any]] = []
    dummy_img = _create_dummy_image_bytes("PNG")

    latest_paint: dict[str, str] = {}

    def mock_json_request(path: str, method: str = "GET", payload: Any = None) -> Any:
        recorded_calls.append((method, path, payload))
        if path == "/api/v1/projects" and method == "POST":
            return {"project": {"id": "proj-123"}}
        if path == "/api/v1/projects/proj-123/analyze":
            return {
                "character_profiles": [
                    {
                        "id": "char-nia",
                        "name": "Nia",
                        "visual_attributes": {"reference_role": "main"},
                    }
                ]
            }
        if path == "/api/v1/projects/proj-123/characters":
            return {
                "characters": [
                    {
                        "id": "char-nia",
                        "name": "Nia",
                        "reference_image_url": "http://ai:8000/storage/fake/char-nia.png",
                    }
                ]
            }
        if "/reference-image" in path:
            return {"job": {"id": "job-char", "status": "done", "actual_cost": 0.01}}
        if "/approve" in path:
            return {"status": "approved"}
        if path == "/api/v1/projects/proj-123/chapters":
            return {"chapters": [{"id": "chap-1"}]}
        if path == "/api/v1/chapters/chap-1/panels":
            return {
                "pages": [{"id": "page-1", "panel_ids": ["p1", "p2", "p3", "p4"]}],
                "panels": [{"id": "p1"}, {"id": "p2"}, {"id": "p3"}, {"id": "p4"}],
            }
        if path == "/api/v1/chapters/chap-1/pages":
            return {
                "pages": [
                    {
                        "id": "page-1",
                        "draft_image_url": "http://ai:8000/storage/fake/draft.png",
                        **latest_paint,
                    }
                ]
            }
        if path == "/api/v1/pages/page-1/draft":
            return {"job": {"id": "job-draft", "status": "done", "actual_cost": 0.01}}
        if path == "/api/v1/pages/page-1/painted-page":
            variant = (payload or {}).get("variant", "draft-conditioned")
            # A receipt carries no image: the settled job's page names the painted image.
            latest_paint.update(
                painted_page_image_job_id=f"job-painted-{variant}",
                painted_page_image_url=f"http://ai:8000/storage/fake/{variant}.png",
            )
            return {
                "job": {"id": f"job-painted-{variant}", "status": "done", "actual_cost": 0.01},
                "job_id": f"job-painted-{variant}",
                "entity_type": "page",
                "variant": variant,
            }
        if "/generation-jobs/" in path:
            return {"job": {"id": "job-id", "status": "done", "actual_cost": 0.01}}
        return {}

    def mock_fetch(url: str, storage_url: str | None = None) -> bytes:
        return dummy_img

    guard = CostGuard(cap=2.0, live=False)
    artifact_dir = tmp_path / "artifacts"
    ink_stub = tmp_path / "ink.png"
    ink_stub.write_bytes(dummy_img)
    target_stub = tmp_path / "target.png"
    target_stub.write_bytes(dummy_img)

    with (
        patch.object(comic_e2e, "json_request", side_effect=mock_json_request),
        patch("quality_compare.json_request", side_effect=mock_json_request),
        patch("quality_compare.fetch_image_bytes", side_effect=mock_fetch),
    ):
        result = run_flow_quality_compare(
            guard=guard,
            artifact_dir=artifact_dir,
            ink_baseline_path=ink_stub,
            target_ref_path=target_stub,
        )

    assert result["page_id"] == "page-1"
    assert (artifact_dir / "variant-a-draft-conditioned.webp").is_file()
    assert (artifact_dir / "variant-a-draft-conditioned.png").is_file()
    assert (artifact_dir / "variant-b-direct-from-brief.webp").is_file()
    assert (artifact_dir / "variant-b-direct-from-brief.png").is_file()
    assert (artifact_dir / "approved-draft.png").is_file()
    assert (artifact_dir / "reference-character.png").is_file()
    assert (artifact_dir / "ink-baseline.png").is_file()
    assert (artifact_dir / "compare.png").is_file()

    # Verify both variants were called with exact parameters
    painted_calls = [
        (method, path, payload)
        for method, path, payload in recorded_calls
        if "/painted-page" in path
    ]
    assert len(painted_calls) == 2
    assert painted_calls[0] == (
        "POST",
        "/api/v1/pages/page-1/painted-page",
        {"variant": "draft-conditioned"},
    )
    assert painted_calls[1] == (
        "POST",
        "/api/v1/pages/page-1/painted-page",
        {"variant": "direct-from-brief"},
    )


def test_main_cli_refusal(tmp_path: Path):
    # Cap exceeding 2.0 is refused
    code = main(["--live", "--cost-cap-usd", "2.5", "--artifact-dir", str(tmp_path)])
    assert code == 2
