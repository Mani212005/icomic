#!/usr/bin/env python3
"""Generate up to eight Icomic poster candidates with an isolated $0.50 cap.

Live use requires POSTER_LIVE=1 and a key supplied in the process environment.
This script never reads .env.local, prints credentials, retries, or records raw
provider responses. Its provider ledger is separate from the comic run ledger.
"""

from __future__ import annotations

import argparse
import json
import os
import sys
from pathlib import Path
from typing import Any

import httpx

REPO_ROOT = Path(__file__).resolve().parents[2]
AI_PATH = REPO_ROOT / "ai"
if str(AI_PATH) not in sys.path:
    sys.path.insert(0, str(AI_PATH))

from app.adapters.image.meta import MetaImageAdapter
from app.adapters.meta_http import MetaHttpClient, MetaConfigurationError
from app.provider_costs import ProviderBudgetError, ProviderCostLedger

HARD_CAP_USD = 0.50
MAX_GENERATIONS = 8
DEFAULT_LEDGER_PATH = REPO_ROOT / ".poster-live-spend.json"
DEFAULT_OUTPUT_DIR = REPO_ROOT / "docs" / "assets" / "poster-candidates"
PROMPT = (
    "Create a portrait watercolor comic poster for Icomic: an open inked comic book "
    "reveals a moonlit city and a young story-maker, deep indigo and teal washes, "
    "warm coral accents, expressive hand-inked panel borders, textured cream paper. "
    "No lettering, words, symbols that resemble letters, logos, or watermark."
)


def validate_count(count: int) -> int:
    if isinstance(count, bool) or not 1 <= count <= MAX_GENERATIONS:
        raise ValueError(f"generation count must be between 1 and {MAX_GENERATIONS}")
    return count


def sanitized_failure(
    exc: Exception, adapter: MetaImageAdapter | None
) -> dict[str, Any]:
    """Keep only response metadata; exception details can contain raw response text."""
    return {
        "status": "failed",
        "error_type": type(exc).__name__,
        "http_status": getattr(exc, "status_code", None)
        or (adapter.last_status_code if adapter else None),
        "response_keys": list(adapter.last_response_keys) if adapter else [],
    }


def generate_candidates(
    *,
    count: int,
    api_key: str,
    ledger_path: Path,
    output_dir: Path,
    base_url: str | None = None,
    transport: httpx.BaseTransport | None = None,
) -> list[dict[str, Any]]:
    """Generate sequential candidates, stopping immediately after any API error."""
    validate_count(count)
    if not api_key or not api_key.strip():
        raise ValueError("poster generation requires a key from the process environment")
    ledger = ProviderCostLedger(
        cap=HARD_CAP_USD,
        prior=0,
        reserve=0,
        path=ledger_path,
        run_id="icomic-poster",
    )
    output_dir.mkdir(parents=True, exist_ok=True)
    results: list[dict[str, Any]] = []

    for index in range(1, count + 1):
        adapter: MetaImageAdapter | None = None
        try:
            with MetaHttpClient(
                api_key=api_key,
                base_url=base_url,
                transport=transport,
                max_retries=0,
                timeout=60,
                cost_ledger=ledger,
                cost_stage="image",
            ) as client:
                adapter = MetaImageAdapter(model_id="muse-image-1.0", http_client=client)
                image = adapter.generate(
                    prompt=PROMPT,
                    reference_images=[],
                    hashed_user_id="icomic-poster-artifact",
                    style="watercolor comic poster, hand-inked panel art",
                )
        except (ProviderBudgetError, MetaConfigurationError) as exc:
            results.append({"status": "refused", "error_type": type(exc).__name__})
            break
        except Exception as exc:  # noqa: BLE001 - manual task must stop on any failure
            results.append(sanitized_failure(exc, adapter))
            break

        suffix = {"image/png": ".png", "image/jpeg": ".jpg", "image/webp": ".webp"}.get(
            image.media_type or "", ".bin"
        )
        destination = output_dir / f"poster-{index:02d}{suffix}"
        destination.write_bytes(image.image_bytes)
        results.append(
            {
                "status": "saved",
                "candidate": destination.name,
                "media_type": image.media_type,
                "bytes": len(image.image_bytes),
                "request_id": image.request_id,
            }
        )
    return results


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--count", type=int, default=1)
    parser.add_argument("--ledger", type=Path, default=DEFAULT_LEDGER_PATH)
    parser.add_argument("--output-dir", type=Path, default=DEFAULT_OUTPUT_DIR)
    args = parser.parse_args(argv)
    try:
        validate_count(args.count)
    except ValueError as exc:
        print(str(exc), file=sys.stderr)
        return 2
    if os.environ.get("POSTER_LIVE") != "1":
        print("Refusing live poster generation: set POSTER_LIVE=1 explicitly.", file=sys.stderr)
        return 2
    api_key = os.environ.get("META_API_KEY", "")
    if not api_key.strip():
        print("Refusing live poster generation: key is unavailable in the environment.", file=sys.stderr)
        return 2

    try:
        results = generate_candidates(
            count=args.count,
            api_key=api_key,
            ledger_path=args.ledger,
            output_dir=args.output_dir,
        )
    except (OSError, ProviderBudgetError, ValueError) as exc:
        print(f"Poster run stopped: {type(exc).__name__}", file=sys.stderr)
        return 1
    print(json.dumps({"candidates": results}, sort_keys=True))
    return 1 if results and results[-1]["status"] != "saved" else 0


if __name__ == "__main__":
    raise SystemExit(main())
