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
- One call per stage, no retries (max_retries=0) or fallback calls.
- Stages continue independently after failures; auth/configuration and budget aborts stop all calls.
- NEVER prints API keys or full model prompts/responses.
- Outputs a single machine-readable status summary line for the supervisor.
"""

from __future__ import annotations

import json
import math
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

from app.adapters.image.meta import MetaImageAdapter
from app.adapters.language.meta import MetaLanguageAdapter
from app.adapters.language.port import Message
from app.adapters.meta_http import (
    MetaConfigurationError,
    MetaHttpClient,
    detect_image_media_type,
    load_reasoning_config,
    validate_model_id_tier,
)
from app.adapters.vision.meta import MetaVisionAdapter
from app.costs import (
    GENERATION_POLICIES,
    PRICE_PER_IMAGE_FLAT_USD,
    WORST_CASE_TEXT_INPUT_TOKENS,
    WORST_CASE_TEXT_OUTPUT_TOKENS,
    WORST_CASE_VISION_INPUT_TOKENS,
    WORST_CASE_VISION_OUTPUT_TOKENS,
    calculate_checklist_worst_case,
    calculate_token_cost,
    estimate_call_worst_case,
)

# ---------------------------------------------------------------------------
# Pricing & Budget Constants
# ---------------------------------------------------------------------------

DEFAULT_BUDGET_USD: float = 5.00
MAX_CHECKLIST_COST_USD: float = 0.05

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
                    if not line.strip():
                        continue
                    record = json.loads(line)
                    cost = float(record["estimated_cost_usd"])
                    if not math.isfinite(cost) or cost < 0:
                        raise ValueError("Invalid ledger cost")
                    records.append(record)
                    total_spend += cost
            if not math.isfinite(total_spend):
                raise ValueError("Invalid cumulative ledger spend")
        except (OSError, ValueError, TypeError, KeyError) as exc:
            raise PermissionError("Cannot safely read cumulative spend ledger") from exc

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
        ts = timestamp_fn() if timestamp_fn else datetime.now(timezone.utc).isoformat()
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

    Refuses non-finite, non-positive values and anything above the $5 hard cap.
    """
    if budget_env is None or budget_env.strip() == "":
        return DEFAULT_BUDGET_USD

    try:
        val = float(budget_env.strip())
    except ValueError as exc:
        raise ValueError("META_LIVE_BUDGET_USD must be a positive number.") from exc

    if not math.isfinite(val) or val <= 0.0:
        raise ValueError("META_LIVE_BUDGET_USD must be positive and finite.")

    if val > DEFAULT_BUDGET_USD:
        raise PermissionError("META_LIVE_BUDGET_USD exceeds the hard $5.00 limit.")

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
            except (OSError, UnicodeError):
                pass

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
        """Run each stage once, stopping all calls only for config/auth or budget errors."""
        diagnostics: dict[str, Any] = {
            "timestamp": datetime.now(timezone.utc).isoformat(),
            "stages": {
                "language": {"stage_status": "SKIPPED"},
                "vision": {"stage_status": "SKIPPED"},
                "image": {"stage_status": "SKIPPED"},
            },
        }
        stages = diagnostics["stages"]
        cumulative_spend = 0.0
        session_spend = 0.0
        attempted_calls = 0

        def save_diagnostics() -> None:
            try:
                self.diagnostics_path.parent.mkdir(parents=True, exist_ok=True)
                with open(self.diagnostics_path, "w", encoding="utf-8") as f:
                    json.dump(diagnostics, f, indent=2)
                    f.write("\n")
            except OSError:
                print("Warning: Failed to write diagnostics file.", file=sys.stderr)

        def finish(stage: str | None = None, reason: str | None = None) -> tuple[bool, str]:
            save_diagnostics()
            language = stages["language"]["stage_status"]
            vision = stages["vision"]["stage_status"]
            image = stages["image"]["stage_status"]
            results = (language, vision, image)
            success = results == ("SUCCESS", "SUCCESS", "SUCCESS") and reason is None
            status = "PASS" if success else "PARTIAL" if "SUCCESS" in results else "FAIL"
            stopped = f" stage={stage} reason={reason}" if reason else ""
            summary = (
                f"STATUS: {status} language={language} vision={vision} image={image}{stopped} "
                f"spend_usd={session_spend:.6f} "
                f"cumulative_spend_usd={cumulative_spend + session_spend:.6f} "
                f"calls={attempted_calls}/3 budget_usd={self.budget_usd:.2f}"
            )
            print(summary)
            return success, summary

        def budget_allows(stage: str, worst_case: float) -> bool:
            if cumulative_spend + session_spend + worst_case > self.budget_usd:
                stages[stage] = {"stage_status": "ABORTED", "reason": "budget_exceeded"}
                return False
            return True

        def record_text_success(stage: str, adapter: Any, text: str, worst_case: float) -> None:
            nonlocal session_spend
            usage = adapter.last_usage
            # If usage is missing, conservatively account for the entire wire cap.
            call_cost = (
                calculate_token_cost(usage.input_tokens, usage.output_tokens)
                if usage
                else worst_case
            )
            self.ledger.record_call(
                model=adapter.default_model,
                adapter=stage,
                cost_usd=call_cost,
                input_tokens=usage.input_tokens if usage else 0,
                output_tokens=usage.output_tokens if usage else 0,
            )
            session_spend += call_cost
            stages[stage] = {
                "status_code": adapter.last_status_code,
                "stage_status": "SUCCESS",
                "top_level_keys": adapter.last_response_keys,
                "finish_reason": adapter.last_finish_reason,
                "usage": {
                    "input_tokens": usage.input_tokens,
                    "output_tokens": usage.output_tokens,
                    "prompt_tokens": usage.input_tokens,
                    "completion_tokens": usage.output_tokens,
                    "total_tokens": usage.total_tokens,
                    "reasoning_tokens": usage.reasoning_tokens,
                }
                if usage
                else None,
                "content_length": len(text),
            }
            save_diagnostics()
            print(f"  - {stage}: SUCCESS cost_usd={call_cost:.6f}")

        def record_failure(
            stage: str,
            model: str,
            exc: Exception,
            worst_case: float,
            sent: bool,
            adapter: Any = None,
        ) -> None:
            nonlocal session_spend
            if sent:
                input_tokens = (
                    WORST_CASE_TEXT_INPUT_TOKENS
                    if stage == "language"
                    else WORST_CASE_VISION_INPUT_TOKENS
                    if stage == "vision"
                    else 0
                )
                output_tokens = (
                    WORST_CASE_TEXT_OUTPUT_TOKENS
                    + max(GENERATION_POLICIES["check_language"].reasoning_reserve, reserve_tokens)
                    if stage == "language"
                    else WORST_CASE_VISION_OUTPUT_TOKENS
                    + max(GENERATION_POLICIES["review"].reasoning_reserve, reserve_tokens)
                    if stage == "vision"
                    else 0
                )
                self.ledger.record_call(
                    model=model,
                    adapter=stage,
                    cost_usd=worst_case,
                    input_tokens=input_tokens,
                    output_tokens=output_tokens,
                    image_count=int(stage == "image"),
                    failed=True,
                )
                session_spend += worst_case
            usage = getattr(exc, "token_usage", None)
            details = getattr(exc, "details", None)
            response_keys = (
                sorted(details)
                if isinstance(details, dict) and details
                else (adapter.last_response_keys if adapter else [])
            )
            stages[stage] = {
                "status_code": getattr(exc, "status_code", None)
                or (adapter.last_status_code if adapter else None),
                "stage_status": "FAILED",
                "error_type": type(exc).__name__,
                "top_level_keys": response_keys,
                "finish_reason": getattr(exc, "finish_reason", None),
                "usage": {
                    "prompt_tokens": usage.input_tokens,
                    "completion_tokens": usage.output_tokens,
                    "total_tokens": usage.total_tokens,
                    "reasoning_tokens": usage.reasoning_tokens,
                }
                if usage
                else None,
                "content_length": 0,
            }
            save_diagnostics()
            # Exception text may contain echoed prompts, response text or credentials.
            print(f"  - {stage}: FAILED ({type(exc).__name__})", file=sys.stderr)

        # Keep this second gate even when the runner is invoked without main().
        if self.transport is None and os.getenv("META_LIVE_CHECK") != "1":
            return finish(reason="safety_gate_active")

        try:
            self.budget_usd = validate_budget(str(self.budget_usd))
            _, reserve_tokens = load_reasoning_config()
            validate_model_id_tier(self.lang_model)
            validate_model_id_tier(self.vis_model)
            validate_model_id_tier(self.img_model)
        except (MetaConfigurationError, ValueError, PermissionError) as exc:
            return finish(stage="configuration", reason=type(exc).__name__)

        checklist_worst_case = calculate_checklist_worst_case(reserve_tokens)
        if checklist_worst_case >= MAX_CHECKLIST_COST_USD:
            return finish(stage="configuration", reason="checklist_cost_limit_exceeded")
        try:
            cumulative_spend, prior_records = self.ledger.read_cumulative_spend()
        except PermissionError:
            return finish(stage="ledger", reason="ledger_unavailable")

        print("Meta AI Adapters - Manual Live Smoke Verification")
        print(f"  Hard Budget: ${self.budget_usd:.2f} USD")
        print(
            f"  Prior Cumulative Spend: ${cumulative_spend:.6f} USD ({len(prior_records)} prior calls)"
        )
        print(f"  Checklist Worst-Case Spend: ${checklist_worst_case:.6f} USD")
        print("  API Key: [FOUND]")

        # Stage 1: Text. One request with a tiny prompt and minimal reasoning.
        worst_case = estimate_call_worst_case("language", reserve_tokens)
        if not budget_allows("language", worst_case):
            return finish(stage="language", reason="budget_exceeded")
        sent = False
        lang_adapter = None
        try:
            with MetaHttpClient(
                api_key=self.api_key,
                base_url=self.base_url,
                transport=self.transport,
                max_retries=0,
                timeout=30.0,
            ) as client:
                lang_adapter = MetaLanguageAdapter(
                    default_model=self.lang_model,
                    http_client=client,
                )
                sent = True
                attempted_calls += 1
                text = lang_adapter.complete(
                    model=self.lang_model,
                    messages=[Message(role="user", content="Say hi.")],
                    **GENERATION_POLICIES["check_language"].options(reserve_tokens),
                )
        except Exception as exc:  # noqa: BLE001 - isolate stage failures, including parser bugs
            record_failure("language", self.lang_model, exc, worst_case, sent, lang_adapter)
            if isinstance(exc, MetaConfigurationError) or getattr(exc, "status_code", None) in (
                401,
                403,
            ):
                return finish(stage="language", reason="configuration_error")
        else:
            record_text_success("language", lang_adapter, text, worst_case)

        # Stage 2: Vision, independent of the text result.
        worst_case = estimate_call_worst_case("vision", reserve_tokens)
        if not budget_allows("vision", worst_case):
            return finish(stage="vision", reason="budget_exceeded")
        sent = False
        vis_adapter = None
        try:
            with MetaHttpClient(
                api_key=self.api_key,
                base_url=self.base_url,
                transport=self.transport,
                max_retries=0,
                timeout=30.0,
            ) as client:
                vis_adapter = MetaVisionAdapter(
                    default_model=self.vis_model,
                    http_client=client,
                )
                sent = True
                attempted_calls += 1
                text = vis_adapter.review(
                    model=self.vis_model,
                    image_bytes=generate_tiny_png(),
                    prompt="Describe briefly.",
                    **GENERATION_POLICIES["review"].options(reserve_tokens),
                )
        except Exception as exc:  # noqa: BLE001 - isolate stage failures, including parser bugs
            record_failure("vision", self.vis_model, exc, worst_case, sent, vis_adapter)
            if isinstance(exc, MetaConfigurationError) or getattr(exc, "status_code", None) in (
                401,
                403,
            ):
                return finish(stage="vision", reason="configuration_error")
        else:
            record_text_success("vision", vis_adapter, text, worst_case)

        # Stage 3: Image, independent of both chat results.
        worst_case = estimate_call_worst_case("image")
        if not budget_allows("image", worst_case):
            return finish(stage="image", reason="budget_exceeded")
        sent = False
        img_adapter = None
        try:
            with MetaHttpClient(
                api_key=self.api_key,
                base_url=self.base_url,
                transport=self.transport,
                max_retries=0,
                timeout=45.0,
            ) as client:
                img_adapter = MetaImageAdapter(model_id=self.img_model, http_client=client)
                sent = True
                attempted_calls += 1
                result = img_adapter.generate(
                    prompt="Single white circle centered on solid black background",
                    reference_images=[],
                    hashed_user_id="manual-smoke-check-user-hash",
                    style="minimalist",
                )
        except Exception as exc:  # noqa: BLE001 - isolate stage failures, including parser bugs
            record_failure("image", self.img_model, exc, worst_case, sent, img_adapter)
            stages["image"].update(
                {
                    "first_12_bytes_hex": None,
                    "detected_media_type": None,
                    "image_dimensions": None,
                    "provenance_keys": [],
                }
            )
            if isinstance(exc, MetaConfigurationError) or getattr(exc, "status_code", None) in (
                401,
                403,
            ):
                return finish(stage="image", reason="configuration_error")
        else:
            self.ledger.record_call(
                model=self.img_model,
                adapter="image",
                cost_usd=worst_case,
                image_count=1,
            )
            session_spend += worst_case
            prov_signals = ("provenance", "c2pa", "watermark", "signature", "manifest")
            stages["image"] = {
                "status_code": img_adapter.last_status_code,
                "stage_status": "SUCCESS",
                "top_level_keys": img_adapter.last_response_keys,
                "content_length": len(result.image_bytes),
                "first_12_bytes_hex": result.image_bytes[:12].hex(),
                "detected_media_type": detect_image_media_type(result.image_bytes),
                "image_dimensions": parse_image_dimensions(result.image_bytes),
                "provenance_keys": [
                    k
                    for k in img_adapter.last_response_keys
                    if any(sig in k.lower() for sig in prov_signals)
                ],
                "c2pa_marker_detected": result.provenance_metadata.get("c2pa", {}).get(
                    "manifest_marker_detected", False
                ),
            }
            save_diagnostics()
            print(f"  - image: SUCCESS cost_usd={worst_case:.6f}")

        return finish()


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
