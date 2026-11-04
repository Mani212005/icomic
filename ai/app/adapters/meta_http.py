"""Shared HTTP client, configuration, rate limiting, and error mapping for Meta AI adapters."""

from __future__ import annotations

import base64
import json
import logging
import os
import random
import re
import struct
import threading
import time
from collections.abc import Callable, Mapping
from typing import Any

import httpx
from typing_extensions import Self

from ..provider_costs import ProviderBudgetError, ProviderCostLedger, token_output_bound
from ..provider_diagnostics import ProviderDiagnostics, last_response
from ..provider_output import ProviderOutputError, reject_schema_echo
from .image.port import ImageResult
from .language.port import Message, TokenUsage
from .vision.port import ColourItem, PaletteSuggestion

logger = logging.getLogger(__name__)

# Base URL - defined in EXACTLY THIS MODULE per specification
DEFAULT_META_API_BASE_URL: str = "https://api.meta.ai/v1"

# Default Model IDs
DEFAULT_META_LANGUAGE_MODEL: str = "muse-spark-1.3"
DEFAULT_META_VISION_MODEL: str = "muse-spark-1.3"
DEFAULT_META_IMAGE_MODEL: str = "muse-image-1.0"

# Default API parameters
DEFAULT_META_USER_FIELD: str = "user"
DEFAULT_IMAGE_RPM_LIMIT: int = 150
DEFAULT_IMAGE_COST_USD: float = 0.01
DEFAULT_HTTP_TIMEOUT: float = 60.0
DEFAULT_MAX_RETRIES: int = 3
DEFAULT_BASE_BACKOFF: float = 0.5
DEFAULT_MAX_BACKOFF: float = 8.0

# Third-party summaries of Meta's API describe these values; a live check must confirm them.
META_REASONING_ALLOWED: tuple[str, ...] = ("minimal", "low", "medium", "high", "xhigh")
DEFAULT_REASONING_EFFORT: str = "low"
# Reasoning shares the output cap and price with visible answer tokens.
DEFAULT_REASONING_RESERVE_TOKENS: int = 1024


# ---------------------------------------------------------------------------
# Exceptions & Error Mapping (docs/META_SWAP.md Section 4.2)
# ---------------------------------------------------------------------------


class MetaAPIError(Exception):
    """Base exception for Meta API adapter operations."""

    def __init__(
        self,
        message: str,
        status_code: int | None = None,
        details: dict[str, Any] | None = None,
        request_id: str | None = None,
    ) -> None:
        super().__init__(message)
        self.message = message
        self.status_code = status_code
        self.details = details or {}
        self.request_id = request_id


class MetaContentPolicyError(MetaAPIError):
    """Structured domain content-policy violation error (HTTP 400).

    Caught and handled gracefully by the Go backend without crashing.
    """

    def __init__(
        self,
        message: str,
        policy_category: str | None = None,
        details: dict[str, Any] | None = None,
        request_id: str | None = None,
    ) -> None:
        super().__init__(message, status_code=400, details=details, request_id=request_id)
        self.policy_category = policy_category


class MetaEmptyResponseError(MetaAPIError):
    """Domain error raised when Meta API returns an empty response content (not retryable)."""

    def __init__(
        self,
        message: str,
        status_code: int | None = 200,
        details: dict[str, Any] | None = None,
        request_id: str | None = None,
        finish_reason: str | None = None,
        token_usage: TokenUsage | None = None,
    ) -> None:
        super().__init__(message, status_code=status_code, details=details, request_id=request_id)
        self.finish_reason = finish_reason
        self.token_usage = token_usage
        self.reasoning_tokens = token_usage.reasoning_tokens if token_usage else None


class MetaRateLimitError(MetaAPIError):
    """Retryable error when rate limit is exceeded (HTTP 429)."""

    def __init__(
        self,
        message: str,
        retry_after: float | None = None,
        details: dict[str, Any] | None = None,
        request_id: str | None = None,
    ) -> None:
        super().__init__(message, status_code=429, details=details, request_id=request_id)
        self.retry_after = retry_after


class MetaConfigurationError(MetaAPIError):
    """Unrecoverable configuration error (HTTP 401/403 or missing credentials)."""

    def __init__(
        self,
        message: str,
        status_code: int | None = None,
        details: dict[str, Any] | None = None,
    ) -> None:
        super().__init__(message, status_code=status_code, details=details)


class MetaTransientError(MetaAPIError):
    """Transient upstream failure (HTTP 5xx or connection/timeout)."""

    def __init__(
        self,
        message: str,
        status_code: int | None = None,
        details: dict[str, Any] | None = None,
        request_id: str | None = None,
    ) -> None:
        super().__init__(message, status_code=status_code, details=details, request_id=request_id)


class MetaUnknownOutcomeError(MetaTransientError):
    """A transmitted POST may have been processed, so automatic retry is unsafe."""

    outcome_unknown = True


# ---------------------------------------------------------------------------
# Reasoning configuration
# ---------------------------------------------------------------------------


def validate_reasoning_effort(effort: Any, *, source: str = "value") -> str:
    """Validate a reasoning_effort string and return it normalised to lower-case.

    Raises MetaConfigurationError for unknown values or the API-rejected ``none``.
    ``source`` is used only in the error message to tell callers where the bad
    value came from (e.g. the env-var name or ``reasoning_effort kwarg``).
    """
    normalised = effort.strip().lower() if isinstance(effort, str) else None
    if normalised == "none":
        raise MetaConfigurationError(
            f"reasoning_effort='none' is rejected by Meta API (HTTP 400). "
            f"Source: {source}. "
            f"Allowed values: {', '.join(META_REASONING_ALLOWED)}."
        )
    if normalised not in META_REASONING_ALLOWED:
        raise MetaConfigurationError(
            f"Invalid reasoning_effort {effort!r} from {source}. "
            f"Allowed values: {', '.join(META_REASONING_ALLOWED)}."
        )
    return normalised


def load_reasoning_config() -> tuple[str, int]:
    """Read and validate reasoning configuration from environment.

    Called at *adapter construction time* (not at module import) so that
    configuration errors surface early, with a clear message, rather than at
    first request.

    Returns:
        (effort, reserve_tokens) - validated reasoning_effort string and
        META_REASONING_RESERVE_TOKENS integer.

    Raises:
        MetaConfigurationError: If either env-var holds an invalid value.
    """
    raw_effort = os.getenv("META_REASONING_EFFORT", DEFAULT_REASONING_EFFORT)
    effort = validate_reasoning_effort(raw_effort, source="META_REASONING_EFFORT env-var")

    reserve_str = os.getenv("META_REASONING_RESERVE_TOKENS", str(DEFAULT_REASONING_RESERVE_TOKENS))
    try:
        reserve = int(reserve_str)
    except ValueError as exc:
        raise MetaConfigurationError(
            f"META_REASONING_RESERVE_TOKENS must be a non-negative integer, got {reserve_str!r}."
        ) from exc
    if reserve < 0:
        raise MetaConfigurationError(
            f"META_REASONING_RESERVE_TOKENS must be non-negative, got {reserve}."
        )
    return effort, reserve


# ---------------------------------------------------------------------------
# Token Bucket Rate Limiter
# ---------------------------------------------------------------------------


class TokenBucket:
    """Thread-safe client-side token bucket rate limiter with injectable clock and sleeper."""

    def __init__(
        self,
        rate_per_minute: float = float(DEFAULT_IMAGE_RPM_LIMIT),
        capacity: float | None = None,
        clock: Callable[[], float] | None = None,
        sleeper: Callable[[float], None] | None = None,
    ) -> None:
        self.rate_per_second = max(0.001, rate_per_minute / 60.0)
        self.capacity = capacity if capacity is not None else float(rate_per_minute)
        self.tokens = self.capacity
        self._clock = clock or time.monotonic
        self._sleeper = sleeper or time.sleep
        self.last_refill = self._clock()
        self._lock = threading.Lock()

    def _refill(self, now: float) -> None:
        elapsed = max(0.0, now - self.last_refill)
        self.last_refill = now
        self.tokens = min(self.capacity, self.tokens + elapsed * self.rate_per_second)

    def acquire(self, tokens: float = 1.0, timeout: float | None = None) -> bool:
        """Acquire tokens, waiting if necessary up to timeout seconds.

        Returns True if acquired, False if timed out.
        """
        deadline = (self._clock() + timeout) if timeout is not None else None

        while True:
            with self._lock:
                now = self._clock()
                self._refill(now)
                if self.tokens >= tokens:
                    self.tokens -= tokens
                    return True

                missing = tokens - self.tokens
                wait_seconds = missing / self.rate_per_second

            if deadline is not None and (now + wait_seconds) > deadline:
                return False

            self._sleeper(min(wait_seconds, 1.0))


# ---------------------------------------------------------------------------
# C2PA Provenance Utilities & Media Type Detection
# ---------------------------------------------------------------------------

C2PA_MARKERS: tuple[bytes, ...] = (b"caBX", b"c2pa", b"jumb", b"C2PA", b"JUMB")


def detect_image_media_type(data: bytes) -> str:
    """Detect real media type from magic numbers without re-encoding or modifying bytes."""
    if data.startswith(b"\x89PNG\r\n\x1a\n"):
        return "image/png"
    if data.startswith(b"\xff\xd8"):
        return "image/jpeg"
    if len(data) >= 12 and data.startswith(b"RIFF") and data[8:12] == b"WEBP":
        return "image/webp"
    if data.startswith((b"GIF87a", b"GIF89a")):
        return "image/gif"
    return "application/octet-stream"


def _detect_jpeg_app11_jumbf(image_bytes: bytes) -> bool:
    """Scan JPEG APP11 marker segments for JUMBF / C2PA boxes without altering bytes."""
    pos = 0
    length = len(image_bytes)
    while pos < length - 3:
        if image_bytes[pos] == 0xFF and image_bytes[pos + 1] == 0xEB:  # APP11
            seg_len = struct.unpack(">H", image_bytes[pos + 2 : pos + 4])[0]
            seg_data = image_bytes[pos + 4 : pos + 2 + seg_len]
            if (
                seg_data.startswith(b"JP")
                or b"jumb" in seg_data
                or b"JUMB" in seg_data
                or b"c2pa" in seg_data
                or b"C2PA" in seg_data
                or b"caBX" in seg_data
            ):
                return True
            pos += 2 + seg_len
        else:
            pos += 1
    return False


def _detect_webp_jumbf(image_bytes: bytes) -> bool:
    """Scan WebP RIFF chunks for JUMBF / C2PA boxes without altering bytes."""
    if len(image_bytes) < 12 or not image_bytes.startswith(b"RIFF") or image_bytes[8:12] != b"WEBP":
        return False
    pos = 12
    length = len(image_bytes)
    while pos + 8 <= length:
        fourcc = image_bytes[pos : pos + 4]
        chunk_len = struct.unpack("<I", image_bytes[pos + 4 : pos + 8])[0]
        if fourcc in (b"JUMB", b"jumb", b"C2PA", b"c2pa"):
            return True
        chunk_data = image_bytes[pos + 8 : pos + 8 + chunk_len]
        if any(marker in chunk_data for marker in C2PA_MARKERS):
            return True
        pos += 8 + chunk_len + (chunk_len % 2)
    return False


def detect_c2pa_markers(image_bytes: bytes) -> bool:
    """Detect presence of C2PA manifest markers in raw image bytes.

    Looks for C2PA marker substrings, JUMBF boxes in JPEG APP11 segments,
    and JUMBF/C2PA chunks in WebP containers. Never alters or touches bytes.
    """
    if any(marker in image_bytes for marker in C2PA_MARKERS):
        return True
    if image_bytes.startswith(b"\xff\xd8") and _detect_jpeg_app11_jumbf(image_bytes):
        return True
    return bool(image_bytes.startswith(b"RIFF") and _detect_webp_jumbf(image_bytes))


def extract_provenance(
    response_data: dict[str, Any],
    image_bytes: bytes,
    model_id: str,
) -> dict[str, Any]:
    """Preserve provenance metadata from response fields and embedded markers.

    Never alters or strips raw bytes.
    """
    has_manifest = detect_c2pa_markers(image_bytes)

    # Check for provenance metadata directly in response
    meta_provenance = (
        response_data.get("provenance")
        or response_data.get("c2pa")
        or response_data.get("provenance_metadata")
    )

    provenance: dict[str, Any] = {}
    if isinstance(meta_provenance, dict):
        provenance = dict(meta_provenance)

    # Ensure C2PA information is structured
    c2pa_info = dict(provenance.get("c2pa", {})) if isinstance(provenance.get("c2pa"), dict) else {}
    c2pa_info.setdefault(
        "watermark_present", has_manifest or c2pa_info.get("watermark_present", True)
    )
    c2pa_info.setdefault("manifest_marker_detected", has_manifest)
    if "claim_generator" not in c2pa_info:
        c2pa_info["claim_generator"] = "Meta-AI-Provenance/1.0"

    provenance["c2pa"] = c2pa_info
    provenance["model"] = model_id
    provenance["cost_usd"] = DEFAULT_IMAGE_COST_USD
    provenance["media_type"] = detect_image_media_type(image_bytes)

    # Capture token usage if present
    usage = response_data.get("usage")
    if isinstance(usage, dict):
        provenance["usage"] = usage

    return provenance


# ---------------------------------------------------------------------------
# Pure Request Building & Response Parsing Functions
# ---------------------------------------------------------------------------


def validate_model_id_tier(model_id: str) -> None:
    """Validate that model does not use the contributor tier unless explicitly permitted.

    Contributor tier trains upstream Meta models on user prompts and completions.
    """
    if "contributor" in model_id.lower() and os.getenv("META_ALLOW_CONTRIBUTOR_TIER") != "1":
        raise ValueError(
            f"Model {model_id!r} is rejected: contributor tier trains Meta models on user prompts and completions. "
            "To permit data collection for upstream training, set META_ALLOW_CONTRIBUTOR_TIER=1."
        )


def build_chat_completion_request(
    model: str,
    messages: list[Message] | list[dict[str, Any]],
    user: str | None = None,
    user_field: str = DEFAULT_META_USER_FIELD,
    reasoning_effort: str | None = None,
    reserve_tokens: int | None = None,
    **kwargs: Any,
) -> dict[str, Any]:
    """Pure function to build chat completion request payload.

    Args:
        model: The model identifier.
        messages: Conversation messages.
        user: Optional hashed user ID.
        user_field: Field name to use for the user ID in the payload.
        reasoning_effort: If supplied, sent as ``reasoning_effort`` in the
            payload.  Must be one of ``META_REASONING_ALLOWED``.  When
            ``None`` the field is omitted (model-determined reasoning).
        reserve_tokens: When a ``max_tokens`` kwarg is also provided, this
            many extra tokens are added to the wire value to reduce answer
            starvation by hidden reasoning.  The caller's ``max_tokens`` is the
            *intended answer length*; ``reserve_tokens`` is transparent to them.
        **kwargs: Additional payload fields (e.g. ``temperature``, ``max_tokens``).
    """
    formatted_messages: list[dict[str, Any]] = []
    for m in messages:
        if isinstance(m, Message):
            formatted_messages.append({"role": m.role, "content": m.content})
        elif isinstance(m, dict):
            formatted_messages.append(m)
        else:
            raise TypeError(f"Unsupported message type: {type(m)}")

    payload: dict[str, Any] = {
        "model": model,
        "messages": formatted_messages,
    }

    # None deliberately omits reasoning_effort for model-determined reasoning.
    if reasoning_effort is not None:
        payload["reasoning_effort"] = reasoning_effort

    # Add reasoning headroom to the shared output cap.
    if "max_tokens" in kwargs and kwargs["max_tokens"] is not None:
        caller_max = kwargs.pop("max_tokens")
        if isinstance(caller_max, bool) or not isinstance(caller_max, int) or caller_max < 0:
            raise MetaConfigurationError("max_tokens must be a non-negative integer.")
        effective_reserve = reserve_tokens if reserve_tokens is not None else 0
        payload["max_tokens"] = caller_max + effective_reserve
    else:
        kwargs.pop("max_tokens", None)  # Drop None value if present

    if not formatted_messages or not any(
        m.get("role") in {"user", "tool"} for m in formatted_messages
    ):
        raise MetaConfigurationError("messages must contain at least one user or tool message")
    for message in formatted_messages:
        if message.get("role") not in {"system", "developer", "user", "assistant", "tool"}:
            raise MetaConfigurationError("Unsupported chat message role")
        if not message.get("content"):
            raise MetaConfigurationError("Chat message content must not be empty")
        if isinstance(message["content"], list):
            for part in message["content"]:
                if not isinstance(part, dict) or part.get("type") not in {"text", "image_url"}:
                    raise MetaConfigurationError("Unsupported chat content part")
                if part["type"] == "text" and not str(part.get("text", "")).strip():
                    raise MetaConfigurationError("Chat text parts must not be empty")
                if part["type"] == "image_url":
                    if message["role"] != "user":
                        raise MetaConfigurationError("Image content requires the user role")
                    image_url = part.get("image_url")
                    url = image_url.get("url") if isinstance(image_url, dict) else None
                    if not isinstance(url, str) or not url.startswith(
                        ("data:", "https://", "http://")
                    ):
                        raise MetaConfigurationError("Invalid image URL content part")
                    if url.startswith("data:"):
                        try:
                            header, encoded = url.split(",", 1)
                            raw = base64.b64decode(encoded, validate=True)
                        except (ValueError, TypeError) as exc:
                            raise MetaConfigurationError("Invalid image data URL") from exc
                        media_type = detect_image_media_type(raw)
                        if (
                            media_type == "application/octet-stream"
                            or header != f"data:{media_type};base64"
                        ):
                            raise MetaConfigurationError(
                                "Image data URL media type disagrees with its bytes"
                            )

    # The adapters implement non-streaming, single-completion text/vision calls.
    if set(kwargs) - {"temperature", "top_p", "seed", "stop", "response_format"}:
        raise MetaConfigurationError("Unsupported chat completion parameter")

    if user:
        payload[user_field] = user
    for k, v in kwargs.items():
        if v is not None and k not in payload:
            payload[k] = v
    return payload


def parse_chat_completion_response(
    data: dict[str, Any],
    headers: Mapping[str, str] | None = None,
) -> tuple[str, TokenUsage, str, dict[str, Any]]:
    """Pure function to parse chat completion response.

    Returns (text_content, token_usage, request_id, usage_metadata).
    Raises MetaEmptyResponseError if response content is empty.
    """
    request_id = str(data.get("id") or (headers.get("x-request-id") if headers else "") or "")

    choices = data.get("choices")
    content = ""
    finish_reason: str | None = None
    if isinstance(choices, list) and len(choices) > 0:
        first_choice = choices[0]
        if isinstance(first_choice, dict):
            finish_reason = first_choice.get("finish_reason")
            message = first_choice.get("message")
            if isinstance(message, dict):
                raw_content = message.get("content")
                if isinstance(raw_content, str):
                    content = raw_content
                elif isinstance(raw_content, list):
                    parts: list[str] = []
                    for part in raw_content:
                        if isinstance(part, str):
                            parts.append(part)
                        elif isinstance(part, dict) and "text" in part:
                            parts.append(str(part["text"]))
                    content = "".join(parts)
                elif raw_content is None:
                    content = ""
            elif "text" in first_choice:
                content = str(first_choice.get("text") or "")

    usage_data = data.get("usage", {})
    if not isinstance(usage_data, dict):
        usage_data = {}

    in_tokens = int(usage_data.get("prompt_tokens", 0))
    # completion_tokens already includes hidden reasoning; never add reasoning_tokens again.
    out_tokens = int(usage_data.get("completion_tokens", 0))
    tot_tokens = int(usage_data.get("total_tokens", in_tokens + out_tokens))

    reasoning_tokens: int | None = None
    if "reasoning_tokens" in usage_data:
        try:
            reasoning_tokens = int(usage_data["reasoning_tokens"])
        except (ValueError, TypeError):
            pass
    elif "completion_tokens_details" in usage_data and isinstance(
        usage_data["completion_tokens_details"], dict
    ):
        details = usage_data["completion_tokens_details"]
        if "reasoning_tokens" in details:
            try:
                reasoning_tokens = int(details["reasoning_tokens"])
            except (ValueError, TypeError):
                pass

    token_usage = TokenUsage(
        input_tokens=in_tokens,
        output_tokens=out_tokens,
        total_tokens=tot_tokens,
        reasoning_tokens=reasoning_tokens,
        finish_reason=finish_reason,
        cached_input_tokens=int(
            (usage_data.get("prompt_tokens_details") or {}).get("cached_tokens", 0)
        ),
    )

    usage_metadata = {
        "request_id": request_id,
        "model": data.get("model", ""),
        "usage": usage_data,
        "finish_reason": finish_reason,
        "reasoning_tokens": reasoning_tokens,
    }

    if not content or not content.strip() or finish_reason == "length":
        raise MetaEmptyResponseError(
            f"Meta API returned empty content or incomplete output (finish_reason={finish_reason!r}, request_id={request_id!r})",
            status_code=200,
            details=data,
            request_id=request_id,
            finish_reason=finish_reason,
            token_usage=token_usage,
        )

    return content, token_usage, request_id, usage_metadata


def build_responses_image_request(
    model: str,
    prompt: str,
    reference_images: list[bytes] | None = None,
    hashed_user_id: str | None = None,
    style: str | None = None,
    user_field: str = DEFAULT_META_USER_FIELD,
    previous_response_id: str | None = None,
) -> dict[str, Any]:
    """Pure function to build Responses API request for image generation."""
    if not prompt.strip():
        raise MetaConfigurationError("Image prompt must not be empty")
    content_parts: list[dict[str, Any]] = []

    if reference_images:
        for ref_bytes in reference_images:
            b64_data = base64.b64encode(ref_bytes).decode("ascii")
            ref_media_type = detect_image_media_type(ref_bytes)
            if ref_media_type not in {"image/png", "image/jpeg", "image/webp", "image/gif"}:
                raise MetaConfigurationError(
                    "Reference must contain image bytes, not a URL or text"
                )
            content_parts.append(
                {
                    "type": "input_image",
                    "image_url": f"data:{ref_media_type};base64,{b64_data}",
                }
            )

    full_prompt = prompt
    if style:
        full_prompt = f"{prompt}. Style: {style}"

    content_parts.append(
        {
            "type": "input_text",
            "text": full_prompt,
        }
    )

    payload: dict[str, Any] = {
        "model": model,
        "input": [
            {
                "role": "user",
                "content": content_parts,
            }
        ],
    }

    if hashed_user_id:
        payload[user_field] = hashed_user_id

    if previous_response_id:
        payload["previous_response_id"] = previous_response_id

    return payload


def parse_responses_image_response(
    data: dict[str, Any],
    model_id: str,
    headers: Mapping[str, str] | None = None,
) -> ImageResult:
    """Pure function to parse Responses API image response into ImageResult.

    Extracts base64 image data, preserves untouched bytes, and extracts provenance.
    """
    request_id = str(data.get("id") or (headers.get("x-request-id") if headers else "") or "")

    image_b64: str | None = None

    # 1. Responses API output format: output items with image_generation_call
    output_items = data.get("output")
    if isinstance(output_items, list):
        for item in output_items:
            if isinstance(item, dict):
                gen_call = item.get("image_generation_call")
                if isinstance(gen_call, dict) and "result" in gen_call:
                    image_b64 = str(gen_call["result"])
                    break
                if "result" in item and isinstance(item["result"], str):
                    image_b64 = item["result"]
                    break

    # 2. Fallback: OpenAI-compatible /v1/images/generations format
    if not image_b64 and isinstance(data.get("data"), list) and len(data["data"]) > 0:
        first = data["data"][0]
        if isinstance(first, dict):
            image_b64 = first.get("b64_json")

    # 3. Direct result / image_bytes key fallback
    if not image_b64 and "image_base64" in data:
        image_b64 = str(data["image_base64"])

    if not image_b64:
        raise MetaAPIError(
            f"No image data found in response for request {request_id}",
            request_id=request_id,
            details=data,
        )

    # Decode raw image bytes - NEVER re-encode or modify
    try:
        raw_bytes = base64.b64decode(image_b64, validate=True)
    except Exception as exc:
        raise MetaAPIError(
            f"Failed to decode base64 image bytes: {exc}",
            request_id=request_id,
        ) from exc

    provenance = extract_provenance(data, raw_bytes, model_id)
    media_type = detect_image_media_type(raw_bytes)
    if media_type == "application/octet-stream":
        raise ProviderOutputError("image", "unrecognized_image_bytes")

    return ImageResult(
        image_bytes=raw_bytes,
        provenance_metadata=provenance,
        model_id=model_id,
        request_id=request_id,
        media_type=media_type,
    )


def parse_palette_json(content: str) -> PaletteSuggestion:
    """Parse JSON palette response from multimodal vision completion."""
    cleaned = content.strip()
    # Strip markdown fenced code blocks if present
    if "```" in cleaned:
        match = re.search(r"```(?:json)?\s*([\s\S]*?)\s*```", cleaned)
        if match:
            cleaned = match.group(1).strip()

    try:
        parsed = json.loads(cleaned)
    except (json.JSONDecodeError, ValueError) as exc:
        raise ProviderOutputError("palette", "invalid_or_incomplete_json") from exc
    reject_schema_echo(parsed, "palette")
    if (
        not isinstance(parsed, dict)
        or not isinstance(parsed.get("theme"), str)
        or not parsed["theme"].strip()
    ):
        raise ProviderOutputError("palette", "schema_mismatch")
    theme = parsed["theme"]
    palette_raw = parsed.get("palette")
    items: list[ColourItem] = []

    if isinstance(palette_raw, list):
        for entry in palette_raw:
            if (
                not isinstance(entry, dict)
                or not all(
                    isinstance(entry.get(k), str) and entry[k].strip()
                    for k in ("region_name", "hex_color", "label")
                )
                or not re.fullmatch(r"#[0-9a-fA-F]{6}", entry["hex_color"])
            ):
                raise ProviderOutputError("palette", "schema_mismatch")
            items.append(
                ColourItem(
                    region_name=entry["region_name"],
                    hex_color=entry["hex_color"].upper(),
                    label=entry["label"],
                )
            )

    if not items:
        raise ProviderOutputError("palette", "empty_palette")

    return PaletteSuggestion(theme=theme, palette=items)


# ---------------------------------------------------------------------------
# Meta HTTP Client
# ---------------------------------------------------------------------------


class MetaHttpClient:
    """HTTP client for Meta API with auth, retries, backoff, and error mapping."""

    def __init__(
        self,
        api_key: str | None = None,
        base_url: str | None = None,
        transport: httpx.BaseTransport | None = None,
        client: httpx.Client | None = None,
        clock: Callable[[], float] | None = None,
        sleeper: Callable[[float], None] | None = None,
        rate_limiter: TokenBucket | None = None,
        max_retries: int = DEFAULT_MAX_RETRIES,
        timeout: float = DEFAULT_HTTP_TIMEOUT,
        cost_ledger: ProviderCostLedger | None = None,
        cost_stage: str = "language",
        diagnostics: ProviderDiagnostics | None = None,
    ) -> None:
        # Lazy read of key from environment if not explicitly passed
        resolved_key = api_key if api_key is not None else os.getenv("META_API_KEY")
        if not resolved_key or not resolved_key.strip():
            raise MetaConfigurationError(
                "Meta API client is not configured: META_API_KEY environment variable is not set. "
                "Set META_API_KEY or use default fake adapters (e.g. ADAPTER_LANGUAGE=fake)."
            )
        self.api_key = resolved_key.strip()

        # Configurable base URL
        resolved_base_url = (
            base_url or os.getenv("META_API_BASE_URL") or DEFAULT_META_API_BASE_URL
        ).rstrip("/")
        self.base_url = resolved_base_url

        self._clock = clock or time.monotonic
        self._sleeper = sleeper or time.sleep
        self.max_retries = 0 if cost_ledger is not None else max_retries
        self.cost_ledger = cost_ledger
        self.cost_stage = cost_stage
        self.diagnostics = diagnostics
        self.timeout = timeout
        self.rate_limiter = rate_limiter

        # Injectable transport/session
        if client is not None:
            self._client = client
        else:
            self._client = httpx.Client(
                base_url=self.base_url,
                transport=transport,
                timeout=self.timeout,
                headers={
                    "Authorization": f"Bearer {self.api_key}",
                    "Content-Type": "application/json",
                    "User-Agent": "icomic-ai/0.1.0",
                },
            )

    def close(self) -> None:
        self._client.close()

    def record_invalid_response(self, error_type: str) -> None:
        if self.diagnostics is not None:
            self.diagnostics.failure(error_type)

    def __enter__(self) -> Self:
        return self

    def __exit__(self, *args: object) -> None:
        self.close()

    def _is_policy_violation(self, status_code: int, body_dict: dict[str, Any]) -> bool:
        """Determine whether HTTP 400 represents a content policy violation."""
        if status_code != 400:
            return False
        err = body_dict.get("error")
        err_str = json.dumps(body_dict).lower()
        policy_signals = (
            "content_policy",
            "policy",
            "safety",
            "moderation",
            "prohibited",
            "terms of service",
        )
        if any(sig in err_str for sig in policy_signals):
            return True
        if isinstance(err, dict):
            code = str(err.get("code", "")).lower()
            if "policy" in code or "safety" in code:
                return True
        return False

    def _parse_retry_after(self, response: httpx.Response) -> float | None:
        """Parse Retry-After header as seconds."""
        header_val = response.headers.get("retry-after")
        if not header_val:
            return None
        try:
            return float(header_val)
        except ValueError:
            return None

    def _count_capped_input(self, payload: dict[str, Any]) -> int:
        """Count the exact single-turn input without generation or billable usage.

        Meta documents /responses/input_tokens as authoritative for text, media,
        and injected prompt scaffolding: https://dev.meta.ai/docs/token-counting.
        No byte-size or fixed image-token estimate can safely replace this count.
        """
        token_output_bound(payload)  # Refuse uncapped calls before any HTTP.
        if set(payload) - {
            "model",
            "messages",
            "max_tokens",
            "reasoning_effort",
            "temperature",
            "top_p",
            "seed",
            "stop",
            "user",
            "response_format",
        }:
            raise ProviderBudgetError("Unsupported parameters in capped input")
        inputs = []
        for message in payload["messages"]:
            if set(message) - {"role", "content"} or message["role"] not in {
                "system",
                "developer",
                "user",
                "assistant",
            }:
                raise ProviderBudgetError("Unsupported message in capped input")
            content = message["content"]
            if isinstance(content, list):
                parts = []
                for part in content:
                    if part["type"] == "text":
                        parts.append({"type": "input_text", "text": part["text"]})
                    elif part["type"] == "image_url":
                        url = part["image_url"]["url"]
                        # Public URLs can change between counting and generation.
                        if set(part["image_url"]) != {"url"} or not url.startswith("data:"):
                            raise ProviderBudgetError(
                                "Capped vision requires immutable inline images"
                            )
                        parts.append({"type": "input_image", "image_url": url})
                    else:
                        raise ProviderBudgetError("Unsupported media in capped input")
                content = parts
            inputs.append({"role": message["role"], "content": content})
        count_payload = {"model": payload["model"], "input": inputs}
        if "response_format" in payload:
            output_format = payload["response_format"]
            if (
                not isinstance(output_format, dict)
                or set(output_format) != {"type", "json_schema"}
                or output_format["type"] != "json_schema"
                or not isinstance(output_format["json_schema"], dict)
                or set(output_format["json_schema"]) - {"name", "schema", "strict", "description"}
                or not isinstance(output_format["json_schema"].get("schema"), dict)
                or not isinstance(output_format["json_schema"].get("name"), str)
            ):
                raise ProviderBudgetError("Unsupported response format in capped input")
            # Responses uses text.format for the same schema supplied to Chat
            # Completions. Include it in preflight so its prompt cost is counted.
            count_payload["text"] = {
                "format": {"type": "json_schema", **output_format["json_schema"]}
            }
        response = self._client.post(
            f"{self.base_url}/responses/input_tokens",
            json=count_payload,
        )
        if self.diagnostics is not None:
            self.diagnostics.capture(
                response.content, response.status_code, self.api_key, "preflight"
            )
        try:
            response.raise_for_status()
            count = response.json()["input_tokens"]
        except (httpx.HTTPError, ValueError, KeyError, TypeError) as exc:
            self.record_invalid_response(type(exc).__name__)
            raise ProviderBudgetError("Input token preflight failed; refusing generation") from exc
        if isinstance(count, bool) or not isinstance(count, int) or count <= 0:
            self.record_invalid_response("InvalidInputTokenCount")
            raise ProviderBudgetError("Invalid input token preflight; refusing generation")
        return count

    def post(
        self,
        endpoint: str,
        json_body: dict[str, Any],
    ) -> tuple[dict[str, Any], Mapping[str, str]]:
        """Execute POST request with safe rejection retries and error mapping.

        Never logs API keys or full prompt bodies.
        """
        if self.diagnostics is not None:
            last_response.set(None)
        input_tokens = None
        if self.cost_ledger is not None:
            # Freeze the same payload for preflight and transmission.
            json_body = json.loads(json.dumps(json_body))
            if self.cost_stage == "image" and (
                endpoint != "/responses" or set(json_body) - {"model", "input", "user"}
            ):
                raise ProviderBudgetError("Capped images require a single-image request")
            if self.cost_stage != "image":
                if endpoint != "/chat/completions":
                    raise ProviderBudgetError("Unsupported capped generation endpoint")
                input_tokens = self._count_capped_input(json_body)

        # Apply client-side token bucket if configured
        if self.rate_limiter is not None:
            self.rate_limiter.acquire(1.0)

        url = (
            endpoint
            if endpoint.startswith("http")
            else f"{self.base_url.rstrip('/')}/{endpoint.lstrip('/')}"
        )
        attempt = 0

        while True:
            attempt += 1
            request_id: str | None = None
            reservation = (
                self.cost_ledger.before_call(self.cost_stage, json_body, input_tokens=input_tokens)
                if self.cost_ledger
                else None
            )
            try:
                try:
                    if self.diagnostics is not None:
                        # A network failure has no response body. Never attribute
                        # a previous call or successful preflight body to it.
                        last_response.set(None)
                    response = self._client.post(url, json=json_body)
                except Exception:
                    if reservation is not None:
                        self.cost_ledger.finish_call(reservation, None)
                    raise
                if self.diagnostics is not None:
                    self.diagnostics.capture(response.content, response.status_code, self.api_key)
                    if not 200 <= response.status_code < 300:
                        self.diagnostics.failure("HTTPError")
                if reservation is not None:
                    try:
                        usage_data = response.json()
                    except (ValueError, json.JSONDecodeError):
                        usage_data = None
                    self.cost_ledger.finish_call(reservation, response.status_code, usage_data)
                request_id = response.headers.get("x-request-id")

                # Successful HTTP 2xx
                if 200 <= response.status_code < 300:
                    try:
                        data = response.json()
                    except Exception as e:
                        raise MetaAPIError(
                            f"Invalid JSON response from Meta API: {e}",
                            status_code=response.status_code,
                            request_id=request_id,
                        ) from e
                    return data, response.headers

                # HTTP 400 - Check for content policy violation
                if response.status_code == 400:
                    try:
                        err_body = response.json()
                    except (json.JSONDecodeError, ValueError):
                        err_body = {"raw": response.text}

                    if self._is_policy_violation(400, err_body):
                        msg = "Generation request blocked by upstream content policy."
                        if isinstance(err_body.get("error"), dict):
                            msg = str(err_body["error"].get("message", msg))
                        elif "message" in err_body:
                            msg = str(err_body["message"])
                        raise MetaContentPolicyError(
                            message=msg,
                            policy_category="upstream_content_policy",
                            details=err_body,
                            request_id=request_id,
                        )
                    raise MetaAPIError(
                        f"Bad Request (HTTP 400): {response.text[:200]}",
                        status_code=400,
                        details=err_body,
                        request_id=request_id,
                    )

                # HTTP 401 or HTTP 403 - Unrecoverable configuration error
                if response.status_code in (401, 403):
                    msg = (
                        f"Authentication or authorization failure (HTTP {response.status_code}). "
                        "Check that META_API_KEY is valid and has required model permissions."
                    )
                    raise MetaConfigurationError(
                        message=msg,
                        status_code=response.status_code,
                        details={"status_code": response.status_code},
                    )

                # HTTP 429 - Rate limit exceeded
                if response.status_code == 429:
                    retry_after = self._parse_retry_after(response)
                    if attempt <= self.max_retries:
                        backoff = (
                            retry_after
                            if retry_after is not None
                            else (DEFAULT_BASE_BACKOFF * (2 ** (attempt - 1)))
                        )
                        jitter = random.uniform(0.05, 0.2)
                        self._sleeper(backoff + jitter)
                        continue
                    raise MetaRateLimitError(
                        "Meta API rate limit exceeded.",
                        retry_after=retry_after,
                        request_id=request_id,
                    )

                # A server error does not prove that generation was not accepted.
                # Preserve the uncertain result for an explicit new user attempt.
                if response.status_code >= 500:
                    raise MetaUnknownOutcomeError(
                        f"Upstream Meta API returned HTTP {response.status_code}; request outcome is unknown. "
                        "Do not retry automatically; start a new attempt only after review.",
                        status_code=response.status_code,
                        request_id=request_id,
                    )

                # Any other unexpected status code
                raise MetaAPIError(
                    f"Unexpected HTTP status {response.status_code} from Meta API.",
                    status_code=response.status_code,
                    request_id=request_id,
                )

            except (httpx.TimeoutException, httpx.NetworkError) as net_err:
                raise MetaUnknownOutcomeError(
                    f"Meta API connection failed and request outcome is unknown: {net_err}. "
                    "Do not retry automatically; start a new attempt only after review.",
                    request_id=request_id,
                ) from net_err
            except Exception as exc:
                self.record_invalid_response(type(exc).__name__)
                raise
