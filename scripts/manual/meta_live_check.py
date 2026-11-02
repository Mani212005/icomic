#!/usr/bin/env python3
"""One-command manual live smoke check script for Meta AI adapters with hard budget and spend ledger.

Excluded from CI and hermetic test runs.
Refuses to run unless META_LIVE_CHECK=1 is explicitly set in the environment.
Makes exactly one minimal call per Meta adapter to verify live API reachability,
authentication, endpoint routing, and payload parsing.

Budget and Pricing Rules (Owner Decisions 2026-11-02):
- Enforces a HARD BUDGET: META_LIVE_BUDGET_USD (default: $5.00 USD).
- Aborts BEFORE any call whose worst-case estimated cost would push cumulative spend past the budget.
- Maintains a persistent spend ledger at .live-spend.jsonl in the repository root.
- Pricing basis (Meta Model API published pricing, Standard Mode, https://api.meta.ai):
  * muse-spark-1.3 input tokens:  $1.25 per 1,000,000 tokens ($0.00000125 / token)
  * muse-spark-1.3 output tokens: $4.25 per 1,000,000 tokens ($0.00000425 / token)
  * muse-image-1.0 image calls:   $0.01 flat per image generated (docs/META_SWAP.md Section 4.5)
- Cheapest path first: Text call -> Vision call -> Image call.
- No loops, no retries (max_retries=0), no fallback calls; stops at the first failure.
- NEVER prints API keys or full model prompts/responses.
- Outputs a single machine-readable status summary line for the supervisor.
"""

from __future__ import annotations

import json
import os
import struct
import sys
import time
import zlib
from collections.abc import Callable
from datetime import datetime, timezone
from pathlib import Path
from typing import Any

import httpx

REPO_ROOT = Path(__file__).resolve().parent.parent.parent
AI_PATH = REPO_ROOT / "ai"
if str(AI_PATH) not in sys.path:
    sys.path.insert(0, str(AI_PATH))

from app.adapters.image.meta import MetaImageAdapter  # noqa: E402
from app.adapters.language.meta import MetaLanguageAdapter  # noqa: E402
from app.adapters.language.port import Message  # noqa: E402
from app.adapters.meta_http import (  # noqa: E402
    MetaHttpClient,
    detect_image_media_type,
)
from app.adapters.vision.meta import MetaVisionAdapter  # noqa: E402

# ---------------------------------------------------------------------------
# Pricing & Budget Constants
# ---------------------------------------------------------------------------

PRICE_INPUT_TOKEN_PER_M: float = 1.25     # $1.25 per 1,000,000 input tokens
PRICE_OUTPUT_TOKEN_PER_M: float = 4.25    # $4.25 per 1,000,000 output tokens
PRICE_PER_IMAGE_FLAT_USD: float = 0.01    # $0.01 per muse-image-1.0 call
DEFAULT_BUDGET_USD: float = 5.00

# Worst-case token estimates for capped calls (64 output tokens for reasoning models)
WORST_CASE_TEXT_INPUT_TOKENS = 60
WORST_CASE_TEXT_OUTPUT_TOKENS = 64
WORST_CASE_VISION_INPUT_TOKENS = 350
WORST_CASE_VISION_OUTPUT_TOKENS = 20

DEFAULT_LEDGER_PATH = REPO_ROOT / ".live-spend.jsonl"
DEFAULT_DIAGNOSTICS_PATH = REPO_ROOT / ".live-diagnostics.json"


def parse_image_dimensions(data: bytes) -> dict[str, int] | None:
    """Parse image dimensions (width, height) without external dependencies."""
    if not data:
        return None
    # PNG: IHDR chunk has width and height at offset 16..24
    if data.startswith(b"\x89PNG\r\n\x1a\n") and len(data) >= 24:
        try:
            w, h = struct.unpack(">II", data[16:24])
            return {"width": w, "height": h}
        except struct.error:
            pass
    # JPEG: Scan SOF markers
    if data.startswith(b"\xff\xd8"):
        pos = 2
        length = len(data)
        while pos < length - 1:
            if data[pos] != 0xFF:
                pos += 1
                continue
            while pos < length and data[pos] == 0xFF:
                pos += 1
            if pos >= length:
                break
            marker = data[pos]
            pos += 1
            if marker in (0xD8, 0xD9):
                continue
            if marker == 0xDA:
                break
            if pos + 2 > length:
                break
            seg_len = struct.unpack(">H", data[pos : pos + 2])[0]
            if seg_len < 2 or pos + seg_len > length:
                break
            if 0xC0 <= marker <= 0xCF and marker not in (0xC4, 0xC8, 0xCC):
                h = struct.unpack(">H", data[pos + 3 : pos + 5])[0]
                w = struct.unpack(">H", data[pos + 5 : pos + 7])[0]
                return {"width": w, "height": h}
            pos += seg_len
    # GIF: Dimensions at offset 6..10
    if data.startswith((b"GIF87a", b"GIF89a")) and len(data) >= 10:
        try:
            w, h = struct.unpack("<HH", data[6:10])
            return {"width": w, "height": h}
        except struct.error:
            pass
    # WebP:
    if len(data) >= 30 and data.startswith(b"RIFF") and data[8:12] == b"WEBP":
        chunk_type = data[12:16]
        if chunk_type == b"VP8 " and len(data) >= 30:
            w, h = struct.unpack("<HH", data[26:30])
            return {"width": w & 0x3FFF, "height": h & 0x3FFF}
        elif chunk_type == b"VP8L" and len(data) >= 25 and data[20] == 0x2F:
            b0, b1, b2, b3 = data[21:25]
            w = 1 + (((b1 & 0x3F) << 8) | b0)
            h = 1 + (((b3 & 0x0F) << 10) | (b2 << 2) | ((b1 & 0xC0) >> 6))
            return {"width": w, "height": h}
        elif chunk_type == b"VP8X" and len(data) >= 30:
            w = 1 + data[24] + (data[25] << 8) + (data[26] << 16)
            h = 1 + data[27] + (data[28] << 8) + (data[29] << 16)
            return {"width": w, "height": h}
    return None


def calculate_token_cost(input_tokens: int, output_tokens: int) -> float:
    """Calculate estimated USD cost for text/vision tokens on muse-spark-1.3."""
    in_cost = (input_tokens * PRICE_INPUT_TOKEN_PER_M) / 1_000_000.0
    out_cost = (output_tokens * PRICE_OUTPUT_TOKEN_PER_M) / 1_000_000.0
    return in_cost + out_cost


def estimate_call_worst_case(stage: str) -> float:
    """Return conservative worst-case cost estimate for a single checklist step."""
    if stage == "language":
        return calculate_token_cost(WORST_CASE_TEXT_INPUT_TOKENS, WORST_CASE_TEXT_OUTPUT_TOKENS)
    if stage == "vision":
        return calculate_token_cost(WORST_CASE_VISION_INPUT_TOKENS, WORST_CASE_VISION_OUTPUT_TOKENS)
    if stage == "image":
        return PRICE_PER_IMAGE_FLAT_USD
    return 0.0


def calculate_checklist_worst_case() -> float:
    """Return total worst-case cost estimate for entire 3-step checklist."""
    return (
        estimate_call_worst_case("language")
        + estimate_call_worst_case("vision")
        + estimate_call_worst_case("image")
    )


# ---------------------------------------------------------------------------
# Spend Ledger Management
# ---------------------------------------------------------------------------


class SpendLedger:
    """Thread-safe persistent spend ledger at .live-spend.jsonl."""

    def __init__(self, ledger_path: Path = DEFAULT_LEDGER_PATH):
        self.ledger_path = ledger_path

    def verify_accessible(self) -> None:
        """Verify ledger file can be read and written, refusing otherwise."""
        try:
            if not self.ledger_path.exists():
                self.ledger_path.parent.mkdir(parents=True, exist_ok=True)
                with open(self.ledger_path, "a", encoding="utf-8") as f:
                    pass
            else:
                with open(self.ledger_path, "r", encoding="utf-8") as f:
                    f.readline()
                with open(self.ledger_path, "a", encoding="utf-8") as f:
                    pass
        except Exception as exc:
            raise PermissionError(
                f"Persistent spend ledger {self.ledger_path} cannot be read or written: {exc}"
            ) from exc

    def read_cumulative_spend(self) -> tuple[float, list[dict[str, Any]]]:
        """Read all existing records and compute running cumulative total spend."""
        self.verify_accessible()
        total_spend = 0.0
        records: list[dict[str, Any]] = []

        if not self.ledger_path.exists():
            return 0.0, records

        try:
            with open(self.ledger_path, "r", encoding="utf-8") as f:
                for line in f:
                    line = line.strip()
                    if not line:
                        continue
                    try:
                        record = json.loads(line)
                        records.append(record)
                        total_spend += float(record.get("estimated_cost_usd", 0.0))
                    except Exception:
                        continue
        except Exception as exc:
            raise PermissionError(f"Failed to read spend ledger: {exc}") from exc

        return total_spend, records

    def record_call(
        self,
        model: str,
        adapter: str,
        cost_usd: float,
        input_tokens: int = 0,
        output_tokens: int = 0,
        image_count: int = 0,
        failed: bool = False,
        timestamp_fn: Callable[[], str] | None = None,
    ) -> dict[str, Any]:
        """Append a call record to the ledger."""
        self.verify_accessible()
        ts = (
            timestamp_fn()
            if timestamp_fn
            else datetime.now(timezone.utc).isoformat()
        )
        record = {
            "timestamp": ts,
            "model": model,
            "adapter": adapter,
            "input_tokens": input_tokens,
            "output_tokens": output_tokens,
            "image_count": image_count,
            "estimated_cost_usd": round(cost_usd, 8),
            "failed": failed,
        }
        with open(self.ledger_path, "a", encoding="utf-8") as f:
            f.write(json.dumps(record) + "\n")
        return record


# ---------------------------------------------------------------------------
# Key Resolution (.env.local Support) & Budget Validation
# ---------------------------------------------------------------------------


def validate_budget(budget_env: str | None) -> float:
    """Validate META_LIVE_BUDGET_USD value.

    Refuses non-numeric or non-positive values.
    Refuses values above $5.00 unless META_LIVE_BUDGET_ALLOW_ABOVE_DEFAULT=1 is set.
    """
    if budget_env is None or budget_env.strip() == "":
        return DEFAULT_BUDGET_USD

    try:
        val = float(budget_env.strip())
    except ValueError as exc:
        raise ValueError(
            f"Invalid META_LIVE_BUDGET_USD {budget_env!r}: must be a positive number."
        ) from exc

    if val <= 0.0:
        raise ValueError(f"Invalid META_LIVE_BUDGET_USD {val}: must be positive.")

    if val > 5.0 and os.getenv("META_LIVE_BUDGET_ALLOW_ABOVE_DEFAULT") != "1":
        raise PermissionError(
            f"META_LIVE_BUDGET_USD of ${val:.2f} exceeds default $5.00 limit. "
            "Increasing the live check budget above $5.00 requires the owner's word: "
            "set META_LIVE_BUDGET_ALLOW_ABOVE_DEFAULT=1."
        )

    return val


def resolve_api_key() -> str:
    """Resolve Meta API key from environment or .env.local file.

    Never logs, echoes, or prints key content.
    """
    key = os.getenv("META_API_KEY", "").strip()
    if key:
        return key

    candidate_paths = [
        REPO_ROOT / ".env.local",
        REPO_ROOT / "projects" / "icomic" / ".env.local",
    ]

    for path in candidate_paths:
        if path.is_file():
            try:
                with open(path, "r", encoding="utf-8") as f:
                    for line in f:
                        line = line.strip()
                        if line.startswith("META_API_KEY="):
                            raw_val = line.split("=", 1)[1].strip()
                            # Strip wrapping quotes if present
                            if (raw_val.startswith('"') and raw_val.endswith('"')) or (
                                raw_val.startswith("'") and raw_val.endswith("'")
                            ):
                                raw_val = raw_val[1:-1]
                            if raw_val:
                                return raw_val
            except Exception:
                continue

    return ""


def generate_tiny_png() -> bytes:
    """Generate a minimal valid 1x1 test PNG."""
    raw_data = bytearray([0, 255, 255, 255])  # RGB white

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


# ---------------------------------------------------------------------------
# Live Check Runner
# ---------------------------------------------------------------------------


class LiveCheckRunner:
    """Executes the 3-step live verification checklist with budget guarding."""

    def __init__(
        self,
        api_key: str,
        base_url: str = "https://api.meta.ai/v1",
        budget_usd: float = DEFAULT_BUDGET_USD,
        ledger: SpendLedger | None = None,
        diagnostics_path: Path | None = None,
        transport: httpx.BaseTransport | None = None,
        clock: Callable[[], float] | None = None,
    ):
        self.api_key = api_key
        self.base_url = base_url
        self.budget_usd = budget_usd
        self.ledger = ledger or SpendLedger()
        self.diagnostics_path = diagnostics_path or DEFAULT_DIAGNOSTICS_PATH
        self.transport = transport
        self._clock = clock or time.monotonic

        self.lang_model = os.getenv("META_LANGUAGE_MODEL", "muse-spark-1.3")
        self.vis_model = os.getenv("META_VISION_MODEL", "muse-spark-1.3")
        self.img_model = os.getenv("META_IMAGE_MODEL", "muse-image-1.0")

    def run(self) -> tuple[bool, str]:
        """Run cheapest-path verification sequence.

        Returns (success: bool, machine_readable_summary: str).
        """
        diagnostics: dict[str, Any] = {
            "timestamp": datetime.now(timezone.utc).isoformat(),
            "stages": {},
        }

        def save_diagnostics() -> None:
            try:
                self.diagnostics_path.parent.mkdir(parents=True, exist_ok=True)
                with open(self.diagnostics_path, "w", encoding="utf-8") as f:
                    json.dump(diagnostics, f, indent=2)
                    f.write("\n")
            except Exception as e:
                print(
                    f"Warning: Failed to write diagnostics file {self.diagnostics_path}: {e}",
                    file=sys.stderr,
                )

        # Ensure ledger accessibility
        try:
            self.ledger.verify_accessible()
        except Exception as exc:
            err_msg = f"Spend ledger error: {exc}"
            print(f"[ABORTED] {err_msg}", file=sys.stderr)
            save_diagnostics()
            summary = f"STATUS: FAIL stage=ledger reason={json.dumps(str(exc))} spend_usd=0.000000 cumulative_spend_usd=0.000000 calls=0/3 budget_usd={self.budget_usd:.2f}"
            print(summary)
            return False, summary

        cumulative_spend, prior_records = self.ledger.read_cumulative_spend()
        checklist_worst_case = calculate_checklist_worst_case()
        session_spend = 0.0
        completed_calls = 0

        print("=" * 65)
        print("Meta AI Adapters - Manual Live Smoke Verification")
        print("=" * 65)
        print("Pricing & Budget Configuration:")
        print(f"  Hard Budget (META_LIVE_BUDGET_USD): ${self.budget_usd:.2f} USD")
        print(f"  Prior Cumulative Spend:             ${cumulative_spend:.6f} USD ({len(prior_records)} prior calls)")
        print(f"  Checklist Worst-Case Spend:         ${checklist_worst_case:.6f} USD")
        print(f"  Base URL:                           {self.base_url}")
        print("  API Key:                            [FOUND]")
        print("=" * 65)

        # Safety gate for live network calls
        if self.transport is None and os.getenv("META_LIVE_CHECK") != "1":
            print(
                "\n[ABORTED] Safety gate active: META_LIVE_CHECK must be '1' for live calls.\n",
                file=sys.stderr,
            )
            save_diagnostics()
            summary = "STATUS: FAIL reason=\"safety_gate_active\" calls=0/3"
            print(summary)
            return False, summary

        # ---------------------------------------------------------------------
        # Stage 1: Text Call (MetaLanguageAdapter) - Cheapest ($~0.0003)
        # ---------------------------------------------------------------------
        stage_worst_case = estimate_call_worst_case("language")
        projected = cumulative_spend + session_spend + stage_worst_case
        if projected > self.budget_usd:
            print(
                f"\n[BUDGET ABORT] Aborting before language call: projected total spend "
                f"${projected:.6f} exceeds budget ceiling ${self.budget_usd:.2f}.",
                file=sys.stderr,
            )
            save_diagnostics()
            summary = f"STATUS: FAIL stage=language reason=\"budget_exceeded\" spend_usd={session_spend:.6f} cumulative_spend_usd={(cumulative_spend + session_spend):.6f} calls={completed_calls}/3 budget_usd={self.budget_usd:.2f}"
            print(summary)
            return False, summary

        print("\n[1/3] Step 1: MetaLanguageAdapter (Text Completion)...")
        try:
            lang_client = MetaHttpClient(
                api_key=self.api_key,
                base_url=self.base_url,
                transport=self.transport,
                max_retries=0,  # Zero retries in live check mode
                timeout=30.0,
            )
            lang_adapter = MetaLanguageAdapter(
                api_key=self.api_key,
                default_model=self.lang_model,
                http_client=lang_client,
            )

            messages = [Message(role="user", content="Reply with a short one-sentence greeting.")]
            response_text = lang_adapter.complete(
                model=self.lang_model,
                messages=messages,
                max_tokens=WORST_CASE_TEXT_OUTPUT_TOKENS,
            )

            in_tokens = lang_adapter.last_usage.input_tokens if lang_adapter.last_usage else 15
            out_tokens = lang_adapter.last_usage.output_tokens if lang_adapter.last_usage else 5
            call_cost = calculate_token_cost(in_tokens, out_tokens)
            session_spend += call_cost
            completed_calls += 1

            self.ledger.record_call(
                model=self.lang_model,
                adapter="language",
                cost_usd=call_cost,
                input_tokens=in_tokens,
                output_tokens=out_tokens,
            )

            diagnostics["stages"]["language"] = {
                "status_code": lang_adapter.last_status_code,
                "stage_status": "SUCCESS",
                "top_level_keys": lang_adapter.last_response_keys,
                "finish_reason": lang_adapter.last_finish_reason,
                "usage": {
                    "input_tokens": in_tokens,
                    "output_tokens": out_tokens,
                    "prompt_tokens": in_tokens,
                    "completion_tokens": out_tokens,
                    "total_tokens": in_tokens + out_tokens,
                    "reasoning_tokens": lang_adapter.last_usage.reasoning_tokens
                    if lang_adapter.last_usage
                    else None,
                },
                "content_length": len(response_text),
            }

            print("  - Status: SUCCESS")
            print(f"  - Response size:   {len(response_text)} chars")
            print(f"  - Request ID:      {lang_adapter.last_request_id or 'none'}")
            print(f"  - Tokens:          {in_tokens} input, {out_tokens} output")
            print(f"  - Step Cost:       ${call_cost:.6f} USD")
            print(f"  - Running Total:   ${(cumulative_spend + session_spend):.6f} USD")
        except Exception as exc:
            fail_cost = stage_worst_case
            session_spend += fail_cost
            self.ledger.record_call(
                model=self.lang_model,
                adapter="language",
                cost_usd=fail_cost,
                input_tokens=WORST_CASE_TEXT_INPUT_TOKENS,
                output_tokens=WORST_CASE_TEXT_OUTPUT_TOKENS,
                failed=True,
            )
            top_keys = (
                sorted(exc.details.keys())
                if isinstance(getattr(exc, "details", None), dict)
                else []
            )
            diagnostics["stages"]["language"] = {
                "status_code": getattr(exc, "status_code", None),
                "stage_status": "FAILED",
                "top_level_keys": top_keys,
                "finish_reason": getattr(exc, "finish_reason", None),
                "usage": {
                    "prompt_tokens": exc.token_usage.input_tokens
                    if getattr(exc, "token_usage", None)
                    else None,
                    "completion_tokens": exc.token_usage.output_tokens
                    if getattr(exc, "token_usage", None)
                    else None,
                    "total_tokens": exc.token_usage.total_tokens
                    if getattr(exc, "token_usage", None)
                    else None,
                    "reasoning_tokens": exc.token_usage.reasoning_tokens
                    if getattr(exc, "token_usage", None)
                    else None,
                }
                if getattr(exc, "token_usage", None)
                else None,
                "content_length": 0,
            }
            save_diagnostics()
            print(f"  - Status: FAILED ({type(exc).__name__}: {exc})", file=sys.stderr)
            summary = f"STATUS: FAIL stage=language reason={json.dumps(str(exc))} spend_usd={session_spend:.6f} cumulative_spend_usd={(cumulative_spend + session_spend):.6f} calls={completed_calls}/3 budget_usd={self.budget_usd:.2f}"
            print(summary)
            return False, summary

        # ---------------------------------------------------------------------
        # Stage 2: Vision Call (MetaVisionAdapter) - Medium ($~0.0005)
        # ---------------------------------------------------------------------
        stage_worst_case = estimate_call_worst_case("vision")
        projected = cumulative_spend + session_spend + stage_worst_case
        if projected > self.budget_usd:
            print(
                f"\n[BUDGET ABORT] Aborting before vision call: projected total spend "
                f"${projected:.6f} exceeds budget ceiling ${self.budget_usd:.2f}.",
                file=sys.stderr,
            )
            save_diagnostics()
            summary = f"STATUS: FAIL stage=vision reason=\"budget_exceeded\" spend_usd={session_spend:.6f} cumulative_spend_usd={(cumulative_spend + session_spend):.6f} calls={completed_calls}/3 budget_usd={self.budget_usd:.2f}"
            print(summary)
            return False, summary

        print("\n[2/3] Step 2: MetaVisionAdapter (Multimodal Line Art Review)...")
        try:
            vis_client = MetaHttpClient(
                api_key=self.api_key,
                base_url=self.base_url,
                transport=self.transport,
                max_retries=0,  # Zero retries in live check mode
                timeout=30.0,
            )
            vis_adapter = MetaVisionAdapter(
                api_key=self.api_key,
                default_model=self.vis_model,
                http_client=vis_client,
            )

            tiny_png = generate_tiny_png()
            review_text = vis_adapter.review(
                model=self.vis_model,
                image_bytes=tiny_png,
                prompt="Brief one-sentence review.",
                max_tokens=WORST_CASE_VISION_OUTPUT_TOKENS,
            )

            in_tokens = vis_adapter.last_usage.input_tokens if vis_adapter.last_usage else 100
            out_tokens = vis_adapter.last_usage.output_tokens if vis_adapter.last_usage else 15
            call_cost = calculate_token_cost(in_tokens, out_tokens)
            session_spend += call_cost
            completed_calls += 1

            self.ledger.record_call(
                model=self.vis_model,
                adapter="vision",
                cost_usd=call_cost,
                input_tokens=in_tokens,
                output_tokens=out_tokens,
            )

            diagnostics["stages"]["vision"] = {
                "status_code": vis_adapter.last_status_code,
                "stage_status": "SUCCESS",
                "top_level_keys": vis_adapter.last_response_keys,
                "finish_reason": vis_adapter.last_finish_reason,
                "usage": {
                    "input_tokens": in_tokens,
                    "output_tokens": out_tokens,
                    "prompt_tokens": in_tokens,
                    "completion_tokens": out_tokens,
                    "total_tokens": in_tokens + out_tokens,
                    "reasoning_tokens": vis_adapter.last_usage.reasoning_tokens
                    if vis_adapter.last_usage
                    else None,
                },
                "content_length": len(review_text),
            }

            print("  - Status: SUCCESS")
            print(f"  - Review size:     {len(review_text)} chars")
            print(f"  - Request ID:      {vis_adapter.last_request_id or 'none'}")
            print(f"  - Tokens:          {in_tokens} input, {out_tokens} output")
            print(f"  - Step Cost:       ${call_cost:.6f} USD")
            print(f"  - Running Total:   ${(cumulative_spend + session_spend):.6f} USD")
        except Exception as exc:
            fail_cost = stage_worst_case
            session_spend += fail_cost
            self.ledger.record_call(
                model=self.vis_model,
                adapter="vision",
                cost_usd=fail_cost,
                input_tokens=WORST_CASE_VISION_INPUT_TOKENS,
                output_tokens=WORST_CASE_VISION_OUTPUT_TOKENS,
                failed=True,
            )
            top_keys = (
                sorted(exc.details.keys())
                if isinstance(getattr(exc, "details", None), dict)
                else []
            )
            diagnostics["stages"]["vision"] = {
                "status_code": getattr(exc, "status_code", None),
                "stage_status": "FAILED",
                "top_level_keys": top_keys,
                "finish_reason": getattr(exc, "finish_reason", None),
                "usage": {
                    "prompt_tokens": exc.token_usage.input_tokens
                    if getattr(exc, "token_usage", None)
                    else None,
                    "completion_tokens": exc.token_usage.output_tokens
                    if getattr(exc, "token_usage", None)
                    else None,
                    "total_tokens": exc.token_usage.total_tokens
                    if getattr(exc, "token_usage", None)
                    else None,
                    "reasoning_tokens": exc.token_usage.reasoning_tokens
                    if getattr(exc, "token_usage", None)
                    else None,
                }
                if getattr(exc, "token_usage", None)
                else None,
                "content_length": 0,
            }
            save_diagnostics()
            print(f"  - Status: FAILED ({type(exc).__name__}: {exc})", file=sys.stderr)
            summary = f"STATUS: FAIL stage=vision reason={json.dumps(str(exc))} spend_usd={session_spend:.6f} cumulative_spend_usd={(cumulative_spend + session_spend):.6f} calls={completed_calls}/3 budget_usd={self.budget_usd:.2f}"
            print(summary)
            return False, summary

        # ---------------------------------------------------------------------
        # Stage 3: Image Call (MetaImageAdapter) - Flat $0.01 per image
        # ---------------------------------------------------------------------
        stage_worst_case = estimate_call_worst_case("image")
        projected = cumulative_spend + session_spend + stage_worst_case
        if projected > self.budget_usd:
            print(
                f"\n[BUDGET ABORT] Aborting before image call: projected total spend "
                f"${projected:.6f} exceeds budget ceiling ${self.budget_usd:.2f}.",
                file=sys.stderr,
            )
            save_diagnostics()
            summary = f"STATUS: FAIL stage=image reason=\"budget_exceeded\" spend_usd={session_spend:.6f} cumulative_spend_usd={(cumulative_spend + session_spend):.6f} calls={completed_calls}/3 budget_usd={self.budget_usd:.2f}"
            print(summary)
            return False, summary

        print("\n[3/3] Step 3: MetaImageAdapter (Responses API muse-image-1.0)...")
        try:
            img_client = MetaHttpClient(
                api_key=self.api_key,
                base_url=self.base_url,
                transport=self.transport,
                max_retries=0,  # Zero retries in live check mode
                timeout=45.0,
            )
            img_adapter = MetaImageAdapter(
                api_key=self.api_key,
                model_id=self.img_model,
                http_client=img_client,
            )

            result = img_adapter.generate(
                prompt="Single white circle centered on solid black background",
                reference_images=[],
                hashed_user_id="manual-smoke-check-user-hash",
                style="minimalist",
            )

            call_cost = PRICE_PER_IMAGE_FLAT_USD
            session_spend += call_cost
            completed_calls += 1

            self.ledger.record_call(
                model=self.img_model,
                adapter="image",
                cost_usd=call_cost,
                image_count=1,
            )

            media_type = detect_image_media_type(result.image_bytes)
            has_c2pa = result.provenance_metadata.get("c2pa", {}).get(
                "manifest_marker_detected", False
            )
            dims = parse_image_dimensions(result.image_bytes)
            prov_signals = ("provenance", "c2pa", "watermark", "signature", "manifest")
            prov_keys = [
                k for k in img_adapter.last_response_keys if any(sig in k.lower() for sig in prov_signals)
            ]

            diagnostics["stages"]["image"] = {
                "status_code": img_adapter.last_status_code,
                "stage_status": "SUCCESS",
                "top_level_keys": img_adapter.last_response_keys,
                "content_length": len(result.image_bytes),
                "first_12_bytes_hex": result.image_bytes[:12].hex()
                if len(result.image_bytes) >= 12
                else result.image_bytes.hex(),
                "detected_media_type": media_type,
                "image_dimensions": dims,
                "provenance_keys": prov_keys,
            }

            print("  - Status: SUCCESS")
            print(f"  - Bytes received:  {len(result.image_bytes)} bytes")
            print(f"  - Media type:      {media_type}")
            print(f"  - C2PA Marker:     {'DETECTED' if has_c2pa else 'NOT DETECTED'}")
            print(f"  - Request ID:      {result.request_id or 'none'}")
            print(f"  - Step Cost:       ${call_cost:.6f} USD")
            print(f"  - Running Total:   ${(cumulative_spend + session_spend):.6f} USD")
        except Exception as exc:
            fail_cost = stage_worst_case
            session_spend += fail_cost
            self.ledger.record_call(
                model=self.img_model,
                adapter="image",
                cost_usd=fail_cost,
                image_count=1,
                failed=True,
            )
            top_keys = (
                sorted(exc.details.keys())
                if isinstance(getattr(exc, "details", None), dict)
                else []
            )
            prov_signals = ("provenance", "c2pa", "watermark", "signature", "manifest")
            prov_keys = [k for k in top_keys if any(sig in k.lower() for sig in prov_signals)]
            diagnostics["stages"]["image"] = {
                "status_code": getattr(exc, "status_code", None),
                "stage_status": "FAILED",
                "top_level_keys": top_keys,
                "content_length": 0,
                "first_12_bytes_hex": None,
                "detected_media_type": None,
                "image_dimensions": None,
                "provenance_keys": prov_keys,
            }
            save_diagnostics()
            print(f"  - Status: FAILED ({type(exc).__name__}: {exc})", file=sys.stderr)
            summary = f"STATUS: FAIL stage=image reason={json.dumps(str(exc))} spend_usd={session_spend:.6f} cumulative_spend_usd={(cumulative_spend + session_spend):.6f} calls={completed_calls}/3 budget_usd={self.budget_usd:.2f}"
            print(summary)
            return False, summary

        # ---------------------------------------------------------------------
        # Final Summary
        # ---------------------------------------------------------------------
        save_diagnostics()
        print("\n" + "=" * 65)
        print("[ALL CHECKS PASSED] Meta AI live integration confirmed working.")
        print(f"Session spend:      ${session_spend:.6f} USD")
        print(f"Cumulative spend:   ${(cumulative_spend + session_spend):.6f} USD")
        print(f"Remaining budget:   ${(self.budget_usd - (cumulative_spend + session_spend)):.6f} USD")

        summary = f"STATUS: PASS spend_usd={session_spend:.6f} cumulative_spend_usd={(cumulative_spend + session_spend):.6f} calls=3/3 budget_usd={self.budget_usd:.2f}"
        print(f"\n{summary}")
        return True, summary


def main() -> int:
    if os.getenv("META_LIVE_CHECK") != "1":
        print(
            "\n[ABORTED] Safety gate active: META_LIVE_CHECK is not set to '1'.\n"
            "This script makes live outbound network requests and spends real API credits.\n"
            "To execute manual verification, run:\n\n"
            "  META_LIVE_CHECK=1 META_API_KEY=<your-key> uv run --project ai python3 scripts/manual/meta_live_check.py\n",
            file=sys.stderr,
        )
        return 1

    try:
        budget_usd = validate_budget(os.getenv("META_LIVE_BUDGET_USD"))
    except (ValueError, PermissionError) as exc:
        print(f"\n[ABORTED] {exc}\n", file=sys.stderr)
        return 1

    # Resolve key from env or .env.local
    api_key = resolve_api_key()
    if not api_key:
        print(
            "\n[ABORTED] META_API_KEY could not be found in environment or .env.local.\n"
            "Please provide a valid Meta API key to run live verification.\n",
            file=sys.stderr,
        )
        return 1

    base_url = os.getenv("META_API_BASE_URL", "https://api.meta.ai/v1")

    runner = LiveCheckRunner(
        api_key=api_key,
        base_url=base_url,
        budget_usd=budget_usd,
    )

    success, _ = runner.run()
    return 0 if success else 1


if __name__ == "__main__":
    sys.exit(main())
