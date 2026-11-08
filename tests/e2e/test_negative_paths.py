"""End-to-end negative path tests for Icomic platform.

Asserts critical negative controls and safety invariants across the stack:
1. Downstream stage generation rejected after editing an approved entity until re-approved (stale cascading).
2. Operator runtime kill switch blocks all image generation stages (returns HTTP 429).
3. Per-user daily image quota exhaustion blocks further generation (returns HTTP 429).
4. Content pre-moderation blocks prohibited prompts (returns HTTP 400 or blocked: true).
"""

from __future__ import annotations

import httpx
import pytest


def test_stale_approval_cascade_blocks_downstream_generation(api_client: httpx.Client) -> None:
    """Downstream generation is rejected when an upstream approved entity is edited, until re-approved."""

    # 1. Create project & run initial stages
    proj_res = api_client.post(
        "/api/v1/projects",
        json={
            "title": "Stale Cascade Test Project",
            "original_script": "Chapter 1: The knight enters the dragon cave.",
            "art_style": "fantasy-graphic",
            "chapter_count": 1,
        },
    )
    assert proj_res.status_code == 201
    proj_id = proj_res.json()["project"]["id"]

    # Analyze & approve characters
    api_client.post(f"/api/v1/projects/{proj_id}/analyze")
    chars_res = api_client.get(f"/api/v1/projects/{proj_id}/characters")
    for char in chars_res.json().get("characters", []):
        ref_res = api_client.post(f"/api/v1/characters/{char['id']}/reference-image")
        assert ref_res.status_code == 202, ref_res.text
        approve_res = api_client.post(f"/api/v1/characters/{char['id']}/approve")
        assert approve_res.status_code == 200, approve_res.text

    # Generate chapters & approve chapter outline
    chap_res = api_client.post(f"/api/v1/projects/{proj_id}/chapters")
    chapters = chap_res.json()["chapters"]
    target_chap = chapters[0]
    chap_id = target_chap["id"]

    app_chap_res = api_client.post(f"/api/v1/chapters/{chap_id}/approve")
    assert app_chap_res.status_code == 200

    # Generate panels and pages
    pan_res = api_client.post(f"/api/v1/chapters/{chap_id}/panels")
    assert pan_res.status_code == 200
    pages = pan_res.json()["pages"]
    page_id = pages[0]["id"]

    # 2. Draft generation succeeds when outline approval is fresh
    draft_ok = api_client.post(f"/api/v1/pages/{page_id}/draft")
    assert draft_ok.status_code == 202, f"Initial draft generation failed: {draft_ok.text}"

    # 3. User edits chapter outline -> makes outline approval stale
    patch_res = api_client.patch(
        f"/api/v1/chapters/{chap_id}",
        json={
            "title": "Updated Chapter Title with New Elements",
            "summary": "Updated summary with major plot shifts.",
        },
    )
    assert patch_res.status_code == 200

    # 4. Downstream draft generation must be REJECTED as stale (HTTP 400)
    draft_stale = api_client.post(f"/api/v1/pages/{page_id}/draft")
    assert draft_stale.status_code == 400, (
        f"Expected HTTP 400 for stale approval, got {draft_stale.status_code}: {draft_stale.text}"
    )
    err_text = draft_stale.text.lower()
    assert "stale" in err_text or "re-approval" in err_text or "approval" in err_text, (
        f"Expected stale error message, got: {draft_stale.text}"
    )

    # 5. Re-approve chapter outline
    reapprove_res = api_client.post(f"/api/v1/chapters/{chap_id}/approve")
    assert reapprove_res.status_code == 200

    # 6. Draft generation now succeeds again (HTTP 202)
    draft_recovered = api_client.post(f"/api/v1/pages/{page_id}/draft")
    assert draft_recovered.status_code == 202, (
        f"Draft generation should succeed after re-approval, got {draft_recovered.status_code}: {draft_recovered.text}"
    )


def test_kill_switch_blocks_generation(kill_switch_server: httpx.Client) -> None:
    """When the operator kill switch is active, all generation requests are blocked with HTTP 429."""

    # Create project and character on kill-switch server
    proj_res = kill_switch_server.post(
        "/api/v1/projects",
        json={
            "title": "Kill Switch Active Test",
            "original_script": "Chapter 1: Deep space mission.",
            "art_style": "sci-fi",
            "chapter_count": 1,
        },
    )
    assert proj_res.status_code == 201
    proj_id = proj_res.json()["project"]["id"]

    kill_switch_server.post(f"/api/v1/projects/{proj_id}/analyze")
    chars_res = kill_switch_server.get(f"/api/v1/projects/{proj_id}/characters")
    chars = chars_res.json().get("characters", [])
    assert len(chars) > 0
    char_id = chars[0]["id"]

    # 1. Character reference generation must be blocked by kill switch
    ref_res = kill_switch_server.post(f"/api/v1/characters/{char_id}/reference-image")
    assert ref_res.status_code == 429, (
        f"Expected HTTP 429 when kill switch active, got {ref_res.status_code}: {ref_res.text}"
    )
    assert "kill switch" in ref_res.text.lower() or "too many requests" in ref_res.text.lower()


def test_quota_exhaustion_blocks_generation(quota_server: httpx.Client) -> None:
    """When a user exceeds their daily image quota, generation requests are blocked with HTTP 429."""

    # Create project and character
    proj_res = quota_server.post(
        "/api/v1/projects",
        json={
            "title": "Quota Exhaustion Test",
            "original_script": "Chapter 1: The speedster runs around the world.",
            "art_style": "comic-standard",
            "chapter_count": 1,
        },
    )
    assert proj_res.status_code == 201
    proj_id = proj_res.json()["project"]["id"]

    quota_server.post(f"/api/v1/projects/{proj_id}/analyze")
    chars_res = quota_server.get(f"/api/v1/projects/{proj_id}/characters")
    chars = chars_res.json().get("characters", [])
    assert len(chars) > 0
    char_id = chars[0]["id"]

    # The hermetic memory server shares the default rolling daily limit.
    quota_limit = 80
    exhausted_res = None
    for i in range(1, quota_limit + 2):
        res = quota_server.post(f"/api/v1/characters/{char_id}/reference-image")
        if res.status_code == 429:
            exhausted_res = res
            break

    assert exhausted_res is not None, (
        f"Expected quota exhaustion HTTP 429 within {quota_limit + 1} generation attempts"
    )
    assert exhausted_res.status_code == 429
    assert "quota" in exhausted_res.text.lower() or "limit reached" in exhausted_res.text.lower(), (
        f"Expected quota error message, got: {exhausted_res.text}"
    )


def test_moderation_blocks_banned_prompt(api_client: httpx.Client) -> None:
    """Content moderation blocks banned/prohibited prompts at both moderation and generation endpoints."""

    # 1. Test dedicated moderation endpoint: POST /api/v1/prompts/moderate
    mod_check_res = api_client.post(
        "/api/v1/prompts/moderate",
        json={"prompt": "A comic script containing extreme violence and bloodshed"},
    )
    assert mod_check_res.status_code == 200, f"Moderation check failed: {mod_check_res.text}"
    mod_data = mod_check_res.json()
    assert mod_data.get("blocked") is True, f"Expected prompt to be blocked, got: {mod_data}"
    assert "extreme violence" in mod_data.get("reason", "").lower()

    # Clean prompt passes
    clean_check_res = api_client.post(
        "/api/v1/prompts/moderate",
        json={"prompt": "A friendly cat adventuring across a peaceful sunny meadow"},
    )
    assert clean_check_res.status_code == 200
    assert clean_check_res.json().get("blocked") is False

    # 2. Test generation stage with banned prompt -> HTTP 400
    proj_res = api_client.post(
        "/api/v1/projects",
        json={
            "title": "Banned Prompt Test",
            "original_script": "Chapter 1: The knight explores a peaceful mountain.",
            "art_style": "clean-comic",
            "chapter_count": 1,
        },
    )
    assert proj_res.status_code == 201
    proj_id = proj_res.json()["project"]["id"]

    api_client.post(f"/api/v1/projects/{proj_id}/analyze")
    chars_res = api_client.get(f"/api/v1/projects/{proj_id}/characters")
    chars = chars_res.json().get("characters", [])
    assert len(chars) > 0
    char_id = chars[0]["id"]

    # Dispatch character generation with a banned keyword in the prompt
    banned_gen_res = api_client.post(
        f"/api/v1/characters/{char_id}/reference-image",
        json={"prompt": "A character executing extreme violence against enemies"},
    )
    assert banned_gen_res.status_code == 400, (
        f"Expected HTTP 400 for banned prompt, got {banned_gen_res.status_code}: {banned_gen_res.text}"
    )
    assert "moderation" in banned_gen_res.text.lower() or "prohibited" in banned_gen_res.text.lower(), (
        f"Expected moderation error message, got: {banned_gen_res.text}"
    )
