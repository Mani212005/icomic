"""Shared HTTP client, configuration, rate limiting, and error mapping for Meta AI adapters."""

from __future__ import annotations

import base64
import json
import logging
import os
import random
import re
import threading
import time
from collections.abc import Callable, Mapping
from typing import Any

import httpx
from typing_extensions import Self

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
# C2PA Provenance Utilities
# ---------------------------------------------------------------------------

C2PA_MARKERS: tuple[bytes, ...] = (b"caBX", b"c2pa", b"jumb", b"C2PA")


def detect_c2pa_markers(image_bytes: bytes) -> bool:
    """Detect presence of C2PA manifest markers in raw image bytes."""
    return any(marker in image_bytes for marker in C2PA_MARKERS)


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
    **kwargs: Any,
) -> dict[str, Any]:
    """Pure function to build chat completion request payload."""
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
    """
    request_id = str(data.get("id") or (headers.get("x-request-id") if headers else "") or "")

    choices = data.get("choices")
    content = ""
    if isinstance(choices, list) and len(choices) > 0:
        first_choice = choices[0]
        if isinstance(first_choice, dict):
            message = first_choice.get("message")
            if isinstance(message, dict):
                content = str(message.get("content") or "")
            elif "text" in first_choice:
                content = str(first_choice.get("text") or "")

    usage_data = data.get("usage", {})
    if not isinstance(usage_data, dict):
        usage_data = {}

    in_tokens = int(usage_data.get("prompt_tokens", 0))
    out_tokens = int(usage_data.get("completion_tokens", 0))
    tot_tokens = int(usage_data.get("total_tokens", in_tokens + out_tokens))
    token_usage = TokenUsage(
        input_tokens=in_tokens,
        output_tokens=out_tokens,
        total_tokens=tot_tokens,
    )

    usage_metadata = {
        "request_id": request_id,
        "model": data.get("model", ""),
        "usage": usage_data,
    }
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
    content_parts: list[dict[str, Any]] = []

    if reference_images:
        for ref_bytes in reference_images:
            b64_data = base64.b64encode(ref_bytes).decode("ascii")
            content_parts.append(
                {
                    "type": "input_image",
                    "image_url": f"data:image/png;base64,{b64_data}",
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
        raw_bytes = base64.b64decode(image_b64)
    except Exception as exc:
        raise MetaAPIError(
            f"Failed to decode base64 image bytes: {exc}",
            request_id=request_id,
        ) from exc

    provenance = extract_provenance(data, raw_bytes, model_id)

    return ImageResult(
        image_bytes=raw_bytes,
        provenance_metadata=provenance,
        model_id=model_id,
        request_id=request_id,
    )


def parse_palette_json(content: str) -> PaletteSuggestion:
    """Parse JSON palette response from multimodal vision completion."""
    cleaned = content.strip()
    # Strip markdown fenced code blocks if present
    if cleaned.startswith("```"):
        match = re.search(r"```(?:json)?\s*([\s\S]*?)\s*```", cleaned)
        if match:
            cleaned = match.group(1).strip()

    try:
        parsed = json.loads(cleaned)
    except (json.JSONDecodeError, ValueError):
        # Fallback to finding first JSON object
        brace_start = cleaned.find("{")
        brace_end = cleaned.rfind("}")
        if brace_start != -1 and brace_end > brace_start:
            try:
                parsed = json.loads(cleaned[brace_start : brace_end + 1])
            except (json.JSONDecodeError, ValueError):
                parsed = {}
        else:
            parsed = {}

    theme = str(parsed.get("theme", "Meta Harmonized Palette"))
    palette_raw = parsed.get("palette", [])
    items: list[ColourItem] = []

    if isinstance(palette_raw, list):
        for entry in palette_raw:
            if isinstance(entry, dict):
                reg = str(entry.get("region_name", "region"))
                hex_c = str(entry.get("hex_color", "#FFFFFF")).upper()
                if not re.match(r"^#[0-9A-F]{6}$", hex_c):
                    hex_c = "#808080"
                lbl = str(entry.get("label", reg.replace("_", " ").title()))
                items.append(ColourItem(region_name=reg, hex_color=hex_c, label=lbl))

    if not items:
        items = [
            ColourItem(region_name="primary", hex_color="#2B2D42", label="Primary"),
            ColourItem(region_name="accent", hex_color="#D90429", label="Accent"),
            ColourItem(region_name="background", hex_color="#EDF2F4", label="Background"),
        ]

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
        self.max_retries = max_retries
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

    def post(
        self,
        endpoint: str,
        json_body: dict[str, Any],
    ) -> tuple[dict[str, Any], Mapping[str, str]]:
        """Execute POST request with retries, backoff, and error mapping.

        Never logs API keys or full prompt bodies.
        """
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
            try:
                response = self._client.post(url, json=json_body)
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

                # HTTP 5xx - Upstream server error (transient)
                if response.status_code >= 500:
                    if attempt <= self.max_retries:
                        delay = min(
                            DEFAULT_MAX_BACKOFF, DEFAULT_BASE_BACKOFF * (2 ** (attempt - 1))
                        )
                        jitter = random.uniform(0.05, 0.25)
                        self._sleeper(delay + jitter)
                        continue
                    raise MetaTransientError(
                        f"Upstream Meta API server error (HTTP {response.status_code}) after {attempt} attempts.",
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
                if attempt <= self.max_retries:
                    delay = min(DEFAULT_MAX_BACKOFF, DEFAULT_BASE_BACKOFF * (2 ** (attempt - 1)))
                    jitter = random.uniform(0.05, 0.25)
                    self._sleeper(delay + jitter)
                    continue
                raise MetaTransientError(
                    f"Transient network failure connecting to Meta API: {net_err}",
                    request_id=request_id,
                ) from net_err
