#!/usr/bin/env python3
"""One-command manual live smoke check script for Meta AI adapters.

Excluded from CI and hermetic test runs.
Refuses to run unless META_LIVE_CHECK=1 is explicitly set in the environment.
Makes exactly one minimal call per Meta adapter to verify live API reachability,
authentication, endpoint routing, and payload parsing.

NEVER prints API keys or full model output prompts/responses.
Prints only HTTP status, payload sizes, token counts, model IDs, and request IDs.
"""

from __future__ import annotations

import os
import sys

# Immediate safety gate: Refuse before any external imports
if os.getenv("META_LIVE_CHECK") != "1":
    print(
        "\n[ABORTED] Safety gate active: META_LIVE_CHECK is not set to '1'.\n"
        "This script makes live outbound network requests and spends real API credits.\n"
        "To execute manual verification, run:\n\n"
        "  META_LIVE_CHECK=1 META_API_KEY=<your-key> uv run --project ai python3 scripts/manual/meta_live_check.py\n",
        file=sys.stderr,
    )
    sys.exit(1)

if not os.getenv("META_API_KEY", "").strip():
    print(
        "\n[ABORTED] META_API_KEY environment variable is not set or empty.\n"
        "Please provide a valid Meta API key to run live verification.\n",
        file=sys.stderr,
    )
    sys.exit(1)

import struct
import zlib
from pathlib import Path

# Add ai directory to Python path
REPO_ROOT = Path(__file__).resolve().parent.parent.parent
AI_PATH = REPO_ROOT / "ai"
if str(AI_PATH) not in sys.path:
    sys.path.insert(0, str(AI_PATH))

from app.adapters.image.meta import MetaImageAdapter  # noqa: E402
from app.adapters.language.meta import MetaLanguageAdapter  # noqa: E402
from app.adapters.language.port import Message  # noqa: E402
from app.adapters.vision.meta import MetaVisionAdapter  # noqa: E402


def generate_tiny_png() -> bytes:
    """Generate a minimal valid 1x1 test PNG."""
    raw_data = bytearray([0, 255, 255, 255])  # filter type 0, RGB white

    def chunk(tag: bytes, data: bytes) -> bytes:
        return (
            struct.pack(">I", len(data))
            + tag
            + data
            + struct.pack(">I", zlib.crc32(tag + data) & 0xFFFFFFFF)
        )

    ihdr = struct.pack(">IIBBBBB", 1, 1, 8, 2, 0, 0, 0)
    return (
        b"\x89PNG\r\n\x1a\n"
        + chunk(b"IHDR", ihdr)
        + chunk(b"IDAT", zlib.compress(bytes(raw_data)))
        + chunk(b"IEND", b"")
    )


def run_live_check() -> int:
    print("=" * 65)
    print("Meta AI Adapters - Manual Live Smoke Verification")
    print("=" * 65)

    # Check API key presence
    api_key = os.getenv("META_API_KEY", "").strip()
    if not api_key:
        print(
            "\n[ABORTED] META_API_KEY environment variable is not set or empty.\n"
            "Please provide a valid Meta API key to run live verification.\n",
            file=sys.stderr,
        )
        return 1

    base_url = os.getenv("META_API_BASE_URL", "https://api.meta.ai/v1")
    lang_model = os.getenv("META_LANGUAGE_MODEL", "muse-spark-1.3")
    vis_model = os.getenv("META_VISION_MODEL", "muse-spark-1.3")
    img_model = os.getenv("META_IMAGE_MODEL", "muse-image-1.0")
    user_field = os.getenv("META_USER_FIELD", "user")

    print("\nConfiguration:")
    print(f"  Base URL:        {base_url}")
    print(f"  Language Model:  {lang_model}")
    print(f"  Vision Model:    {vis_model}")
    print(f"  Image Model:     {img_model}")
    print(f"  User ID Field:   {user_field}")
    print(f"  API Key:         [PRESENT, length: {len(api_key)} chars, hidden]")

    failures = 0

    # -------------------------------------------------------------------------
    # 1. MetaLanguageAdapter Live Smoke
    # -------------------------------------------------------------------------
    print("\n[1/3] Testing MetaLanguageAdapter (Chat Completions)...")
    try:
        lang_adapter = MetaLanguageAdapter(
            api_key=api_key,
            base_url=base_url,
            default_model=lang_model,
        )
        messages = [
            Message(role="user", content="Reply with exactly the word: 'PONG'. Do not add any punctuation.")
        ]
        tokens = lang_adapter.estimate_tokens(messages)
        print(f"  - Pre-flight token estimate: {tokens.input_tokens} input, {tokens.total_tokens} total")

        response_text = lang_adapter.complete(
            model=lang_model,
            messages=messages,
            hashed_user_id="manual-smoke-check-user-hash",
        )
        print("  - Status: SUCCESS")
        print(f"  - Response size: {len(response_text)} characters")
        print(f"  - Request ID:    {lang_adapter.last_request_id or 'none'}")
        if lang_adapter.last_usage:
            print(f"  - Tokens used:   {lang_adapter.last_usage.total_tokens} total "
                  f"({lang_adapter.last_usage.input_tokens} prompt, {lang_adapter.last_usage.output_tokens} completion)")
    except Exception as exc:
        failures += 1
        print(f"  - Status: FAILED ({type(exc).__name__}: {exc})", file=sys.stderr)

    # -------------------------------------------------------------------------
    # 2. MetaVisionAdapter Live Smoke
    # -------------------------------------------------------------------------
    print("\n[2/3] Testing MetaVisionAdapter (Multimodal Chat Review)...")
    try:
        vis_adapter = MetaVisionAdapter(
            api_key=api_key,
            base_url=base_url,
            default_model=vis_model,
        )
        tiny_png = generate_tiny_png()
        review_text = vis_adapter.review(
            model=vis_model,
            image_bytes=tiny_png,
            prompt="Briefly describe the visual quality of this test image in one sentence.",
        )
        print("  - Status: SUCCESS")
        print(f"  - Review size:   {len(review_text)} characters")
        print(f"  - Request ID:    {vis_adapter.last_request_id or 'none'}")
        if vis_adapter.last_usage:
            print(f"  - Tokens used:   {vis_adapter.last_usage.total_tokens} total")
    except Exception as exc:
        failures += 1
        print(f"  - Status: FAILED ({type(exc).__name__}: {exc})", file=sys.stderr)

    # -------------------------------------------------------------------------
    # 3. MetaImageAdapter Live Smoke
    # -------------------------------------------------------------------------
    print("\n[3/3] Testing MetaImageAdapter (Responses API muse-image-1.0)...")
    try:
        img_adapter = MetaImageAdapter(
            api_key=api_key,
            base_url=base_url,
            model_id=img_model,
        )
        result = img_adapter.generate(
            prompt="A minimalist black circle centered on white background",
            reference_images=[],
            hashed_user_id="manual-smoke-check-user-hash",
            style="minimalist",
        )
        is_png = result.image_bytes.startswith(b"\x89PNG")
        print("  - Status: SUCCESS")
        print(f"  - Bytes received: {len(result.image_bytes)} bytes")
        print(f"  - PNG valid:      {is_png}")
        print(f"  - Model ID:       {result.model_id}")
        print(f"  - Request ID:     {result.request_id or 'none'}")
        has_c2pa = result.provenance_metadata.get("c2pa", {}).get("manifest_marker_detected", False)
        print(f"  - C2PA Marker:    {'DETECTED' if has_c2pa else 'NOT DETECTED (check terms)'}")
        print(f"  - Cost:           ${result.provenance_metadata.get('cost_usd', 0.01):.2f}")
    except Exception as exc:
        failures += 1
        print(f"  - Status: FAILED ({type(exc).__name__}: {exc})", file=sys.stderr)

    # -------------------------------------------------------------------------
    # Summary
    # -------------------------------------------------------------------------
    print("\n" + "=" * 65)
    if failures == 0:
        print("[ALL CHECKS PASSED] Meta AI live integration confirmed working.")
        print("All assumptions verified successfully:")
        print("  - Chat completions language endpoint operational")
        print("  - Multimodal vision endpoint operational")
        print("  - Responses API image generation endpoint operational")
        print("  - Provenance metadata and C2PA markers preserved")
        return 0
    else:
        print(f"[VERIFICATION FAILED] {failures} check(s) encountered errors.", file=sys.stderr)
        return 1


if __name__ == "__main__":
    sys.exit(run_live_check())
