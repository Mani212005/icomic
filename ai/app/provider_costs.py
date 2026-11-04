"""Durable, prompt-free accounting at the provider HTTP boundary for capped E2E runs."""

import fcntl
import json
import os
import threading
from contextlib import contextmanager
from contextvars import ContextVar
from decimal import Decimal
from pathlib import Path

from .costs import (
    PRICE_CACHED_INPUT_TOKEN_PER_M,
    PRICE_INPUT_TOKEN_PER_M,
    PRICE_OUTPUT_TOKEN_PER_M,
    PRICE_PER_IMAGE_FLAT_USD,
)

# Documented for Muse Spark 1.3 at dev.meta.ai/docs/cookbook/long-context.
MUSE_CONTEXT_TOKENS = 1_048_576

provider_operation = ContextVar("provider_operation", default=("unknown", None))


class ProviderBudgetError(ValueError):
    pass


class ProviderAccountingError(RuntimeError):
    pass


def token_output_bound(payload):
    """The wire cap already includes visible output and the reasoning reserve."""
    if payload.get("model") != "muse-spark-1.3":
        raise ProviderBudgetError("Capped token calls require the priced Muse Spark model")
    bound = payload.get("max_tokens")
    if isinstance(bound, bool) or not isinstance(bound, int) or bound <= 0:
        raise ProviderBudgetError("Capped provider calls require a positive max_tokens wire limit")
    if bound > MUSE_CONTEXT_TOKENS:
        raise ProviderBudgetError("Output limit exceeds the Muse context window")
    return bound


class ProviderCostLedger:
    """Reserve before transmission, then replace reservations with observed usage.

    Unknown outcomes retain their full reservation. An atomic file and advisory
    lock protect pending calls across threads, workers, and process restarts.
    The file contains only operation IDs, numeric usage, status and costs.
    """

    def __init__(self, cap, prior=0, reserve=0, path: Path | None = None, run_id="test"):
        self.cap, self.prior, self.reserve = (Decimal(str(n)) for n in (cap, prior, reserve))
        if not all(n.is_finite() for n in (self.cap, self.prior, self.reserve)):
            raise ProviderBudgetError("Cost configuration must be finite")
        if (
            not (0 < self.cap <= 2)
            or min(self.prior, self.reserve) < 0
            or self.prior + self.reserve >= self.cap
        ):
            raise ProviderBudgetError("Invalid shared cost cap, prior spend or third-check reserve")
        self.path = path
        self._lock = threading.RLock()
        self.state = {
            "run_id": run_id,
            "hard_cap_usd": float(self.cap),
            "prior_spend_usd": float(self.prior),
            "third_check_reserve_usd": float(self.reserve),
            "calls": [],
        }
        with self._locked():
            pass  # Fail before provider construction if persistence is unavailable.

    @contextmanager
    def _locked(self):
        with self._lock:
            if self.path is None:
                yield
                return
            self.path.parent.mkdir(parents=True, exist_ok=True)
            with self.path.with_suffix(".lock").open("a") as lock:
                fcntl.flock(lock, fcntl.LOCK_EX)
                if self.path.exists():
                    saved = json.loads(self.path.read_text())
                    for name in (
                        "run_id",
                        "hard_cap_usd",
                        "prior_spend_usd",
                        "third_check_reserve_usd",
                    ):
                        if saved[name] != self.state[name]:
                            raise ProviderBudgetError(
                                "Existing cost ledger belongs to a different run or budget"
                            )
                    self.state = saved
                try:
                    yield
                finally:
                    temporary = self.path.with_suffix(".tmp")
                    with temporary.open("w") as output:
                        json.dump(self._report(), output, indent=2)
                        output.flush()
                        os.fsync(output.fileno())
                    os.replace(temporary, self.path)
                    directory_fd = os.open(self.path.parent, os.O_RDONLY | os.O_DIRECTORY)
                    try:
                        os.fsync(directory_fd)
                    finally:
                        os.close(directory_fd)

    def _report(self):
        known = sum(
            (
                Decimal(str(c["actual_cost_usd"]))
                for c in self.state["calls"]
                if c["actual_cost_usd"] is not None
            ),
            Decimal(0),
        )
        charged = sum(
            (
                Decimal(
                    str(
                        c["actual_cost_usd"]
                        if c["actual_cost_usd"] is not None
                        else c["reserved_cost_usd"]
                    )
                )
                for c in self.state["calls"]
            ),
            Decimal(0),
        )
        return {
            **self.state,
            "actual_spend_usd": float(known),
            "accounted_spend_usd": float(charged),
            "cumulative_accounted_spend_usd": float(self.prior + charged),
            "unpriced_calls": sum(c["actual_cost_usd"] is None for c in self.state["calls"]),
        }

    def report(self):
        with self._locked():
            return self._report()

    def before_call(self, stage, payload, *, input_tokens=None):
        if stage == "image":
            if payload.get("model") != "muse-image-1.0":
                raise ProviderBudgetError("Capped image calls require the priced Muse image model")
            estimate = Decimal(str(PRICE_PER_IMAGE_FLAT_USD))
            input_bound = output_bound = None
        else:
            if stage not in {"language", "vision"} or payload.get("model") != "muse-spark-1.3":
                raise ProviderBudgetError("Capped token calls require the priced Muse Spark model")
            output_bound = token_output_bound(payload)
            # An absent explicit input count keeps the original conservative fallback.
            # The HTTP boundary supplies the model-specific preflight count for the
            # exact immutable request, including media and provider prompt scaffolding.
            input_bound = MUSE_CONTEXT_TOKENS if input_tokens is None else input_tokens
            if (
                isinstance(input_bound, bool)
                or not isinstance(input_bound, int)
                or not 0 < input_bound <= MUSE_CONTEXT_TOKENS
            ):
                raise ProviderBudgetError("Invalid explicit input token bound")
            if input_tokens is not None and input_bound + output_bound > MUSE_CONTEXT_TOKENS:
                raise ProviderBudgetError("Input and output limits exceed the Muse context window")
            estimate = (
                Decimal(input_bound) * Decimal(str(PRICE_INPUT_TOKEN_PER_M))
                + Decimal(output_bound) * Decimal(str(PRICE_OUTPUT_TOKEN_PER_M))
            ) / 1_000_000
        with self._locked():
            accounted = Decimal(str(self._report()["accounted_spend_usd"]))
            if self.prior + accounted + self.reserve + estimate > self.cap:
                raise ProviderBudgetError("Provider cost guard refused a call before transmission")
            operation, job_id = provider_operation.get()
            index = len(self.state["calls"])
            self.state["calls"].append(
                {
                    "stage": stage,
                    "operation": operation,
                    "job_id": job_id,
                    "status": "pending",
                    "http_status": None,
                    "reserved_cost_usd": float(estimate),
                    "actual_cost_usd": None,
                    "input_bound": input_bound,
                    "output_bound": output_bound,
                    "usage": None,
                }
            )
            return index

    def finish_call(self, index, status, data=None):
        with self._locked():
            call = self.state["calls"][index]
            call.update(
                status="completed" if status is not None and 200 <= status < 300 else "failed",
                http_status=status,
            )
            usage = data.get("usage") if isinstance(data, dict) else None
            if call["stage"] == "image" and status is not None and 200 <= status < 300:
                count = 0
                if isinstance(data, dict):
                    outputs = data.get("output")
                    for item in outputs if isinstance(outputs, list) else []:
                        if isinstance(item, dict):
                            image = item.get("image_generation_call", item)
                            if (
                                isinstance(image, dict)
                                and isinstance(image.get("result"), str)
                                and image["result"]
                            ):
                                count += 1
                    if not count:
                        images = data.get("data")
                        if isinstance(images, list):
                            count = sum(
                                isinstance(item, dict) and bool(item.get("b64_json"))
                                for item in images
                            )
                    if not count and data.get("image_base64"):
                        count = 1
                if count:
                    call["image_count"] = count
                    call["actual_cost_usd"] = float(
                        Decimal(count) * Decimal(str(PRICE_PER_IMAGE_FLAT_USD))
                    )
            elif (
                call["status"] == "completed"
                and call["stage"] != "image"
                and isinstance(usage, dict)
            ):
                inputs, outputs = usage.get("prompt_tokens"), usage.get("completion_tokens")
                cached_details = usage.get("prompt_tokens_details") or {}
                cached = (
                    cached_details.get("cached_tokens", 0)
                    if isinstance(cached_details, dict)
                    else None
                )
                if (
                    all(
                        isinstance(n, int) and not isinstance(n, bool) and n >= 0
                        for n in (inputs, outputs)
                    )
                    and isinstance(cached, int)
                    and not isinstance(cached, bool)
                    and 0 <= cached <= inputs
                ):
                    call["usage"] = {
                        "input_tokens": inputs,
                        "output_tokens": outputs,
                        "cached_input_tokens": cached,
                    }
                    call["actual_cost_usd"] = float(
                        (
                            Decimal(inputs - cached) * Decimal(str(PRICE_INPUT_TOKEN_PER_M))
                            + Decimal(cached) * Decimal(str(PRICE_CACHED_INPUT_TOKEN_PER_M))
                            + Decimal(outputs) * Decimal(str(PRICE_OUTPUT_TOKEN_PER_M))
                        )
                        / 1_000_000
                    )
            if call["usage"] is not None and (
                call["usage"]["input_tokens"] > call["input_bound"]
                or call["usage"]["output_tokens"] > call["output_bound"]
            ):
                raise ProviderAccountingError(
                    "Observed usage exceeded its explicit token bounds; stopping"
                )
            if (
                call["actual_cost_usd"] is not None
                and call["actual_cost_usd"] > call["reserved_cost_usd"]
            ):
                raise ProviderAccountingError(
                    "Observed usage exceeded its conservative reservation; stopping"
                )
            if call["status"] == "completed" and call["actual_cost_usd"] is None:
                raise ProviderAccountingError(
                    "Successful provider call returned no usage; reservation retained"
                )
