"""Meta image adapter using the Responses API (muse-image-1.0)."""

from __future__ import annotations

import os
from typing import Any

import httpx

from ..meta_http import (
    DEFAULT_IMAGE_RPM_LIMIT,
    DEFAULT_META_IMAGE_MODEL,
    DEFAULT_META_USER_FIELD,
    MetaConfigurationError,
    MetaHttpClient,
    TokenBucket,
    build_responses_image_request,
    parse_responses_image_response,
    validate_model_id_tier,
)
from .port import ImagePort, ImageResult


class MetaImageAdapter(ImagePort):
    """Real Meta image generation adapter targeting muse-image-1.0 via Responses API."""

    def __init__(
        self,
        api_key: str | None = None,
        base_url: str | None = None,
        model_id: str | None = None,
        http_client: MetaHttpClient | None = None,
        transport: httpx.BaseTransport | None = None,
        rate_limiter: TokenBucket | None = None,
        concurrency: int | None = None,
        **client_kwargs: Any,
    ) -> None:
        self.model_id = model_id or os.getenv("META_IMAGE_MODEL") or DEFAULT_META_IMAGE_MODEL
        validate_model_id_tier(self.model_id)
        self.user_field = os.getenv("META_USER_FIELD", DEFAULT_META_USER_FIELD)

        # Rate limiting: 150 RPM ceiling adjusted for worker concurrency if needed
        rpm_limit = float(os.getenv("IMAGE_RATE_LIMIT_RPM", str(DEFAULT_IMAGE_RPM_LIMIT)))
        self.rate_limiter = rate_limiter or TokenBucket(rate_per_minute=rpm_limit)

        if http_client is not None:
            self._client = http_client
        else:
            try:
                self._client = MetaHttpClient(
                    api_key=api_key,
                    base_url=base_url,
                    transport=transport,
                    rate_limiter=self.rate_limiter,
                    **client_kwargs,
                )
            except MetaConfigurationError as exc:
                raise RuntimeError(f"MetaImageAdapter is not configured: {exc.message}") from exc

        self.last_request_id: str = ""
        self.last_cost: float = 0.01
        self.last_status_code: int | None = None
        self.last_response_keys: list[str] = []
        self.last_response_data: dict[str, Any] = {}

    def generate(
        self,
        prompt: str,
        reference_images: list[bytes],
        hashed_user_id: str,
        style: str,
    ) -> ImageResult:
        """Generate an image from prompt, optional turnaround references, and style descriptors."""
        payload = build_responses_image_request(
            model=self.model_id,
            prompt=prompt,
            reference_images=reference_images,
            hashed_user_id=hashed_user_id,
            style=style,
            user_field=self.user_field,
        )

        data, headers = self._client.post("/responses", payload)
        self.last_status_code = 200
        self.last_response_keys = sorted(data.keys()) if isinstance(data, dict) else []
        self.last_response_data = data
        try:
            result = parse_responses_image_response(data, self.model_id, headers)
        except Exception as exc:
            self._client.record_invalid_response(type(exc).__name__)
            raise

        self.last_request_id = result.request_id
        return result
