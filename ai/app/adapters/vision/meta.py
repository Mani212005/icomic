"""Meta vision adapter for multimodal line art review and colour palette suggestion."""

from __future__ import annotations

import base64
import os
from typing import Any

import httpx

from ..meta_http import (
    DEFAULT_META_USER_FIELD,
    DEFAULT_META_VISION_MODEL,
    MetaConfigurationError,
    MetaEmptyResponseError,
    MetaHttpClient,
    build_chat_completion_request,
    detect_image_media_type,
    load_reasoning_config,
    parse_chat_completion_response,
    parse_palette_json,
    validate_model_id_tier,
    validate_reasoning_effort,
)
from .port import PaletteSuggestion, VisionPort


class MetaVisionAdapter(VisionPort):
    """Real Meta multimodal vision model adapter using image content parts."""

    def __init__(
        self,
        api_key: str | None = None,
        base_url: str | None = None,
        default_model: str | None = None,
        http_client: MetaHttpClient | None = None,
        transport: httpx.BaseTransport | None = None,
        **client_kwargs: Any,
    ) -> None:
        self.default_model = (
            default_model or os.getenv("META_VISION_MODEL") or DEFAULT_META_VISION_MODEL
        )
        validate_model_id_tier(self.default_model)
        self.user_field = os.getenv("META_USER_FIELD", DEFAULT_META_USER_FIELD)

        self._reasoning_effort, self._reasoning_reserve = load_reasoning_config()

        if http_client is not None:
            self._client = http_client
        else:
            try:
                self._client = MetaHttpClient(
                    api_key=api_key,
                    base_url=base_url,
                    transport=transport,
                    **client_kwargs,
                )
            except MetaConfigurationError as exc:
                raise RuntimeError(f"MetaVisionAdapter is not configured: {exc.message}") from exc

        self.last_request_id: str = ""
        self.last_usage = None
        self.last_finish_reason: str | None = None
        self.last_status_code: int | None = None
        self.last_response_keys: list[str] = []

    def review(self, model: str, image_bytes: bytes, prompt: str, **kwargs: Any) -> str:
        """Submit image bytes and review prompt to Meta multimodal vision endpoint."""
        target_model = model or self.default_model
        validate_model_id_tier(target_model)
        b64_image = base64.b64encode(image_bytes).decode("ascii")
        media_type = detect_image_media_type(image_bytes)

        user = kwargs.pop("hashed_user_id", None) or kwargs.pop("user", None)

        call_reasoning_effort = kwargs.pop("reasoning_effort", self._reasoning_effort)
        call_reasoning_effort = validate_reasoning_effort(
            call_reasoning_effort, source="reasoning_effort kwarg"
        )
        call_reserve = kwargs.pop("reasoning_reserve_tokens", self._reasoning_reserve)
        if isinstance(call_reserve, bool) or not isinstance(call_reserve, int) or call_reserve < 0:
            raise MetaConfigurationError("reasoning_reserve_tokens must be a non-negative integer")

        content_parts = [
            {"type": "text", "text": prompt},
            {
                "type": "image_url",
                "image_url": {"url": f"data:{media_type};base64,{b64_image}"},
            },
        ]
        messages = [{"role": "user", "content": content_parts}]

        payload = build_chat_completion_request(
            model=target_model,
            messages=messages,
            user=user,
            user_field=self.user_field,
            reasoning_effort=call_reasoning_effort,
            reserve_tokens=call_reserve,
            **kwargs,
        )

        try:
            data, headers = self._client.post("/chat/completions", payload)
            self.last_status_code = 200
            self.last_response_keys = sorted(data.keys()) if isinstance(data, dict) else []
            content, token_usage, request_id, _ = parse_chat_completion_response(data, headers)

            self.last_request_id = request_id
            self.last_usage = token_usage
            self.last_finish_reason = token_usage.finish_reason
            return content
        except MetaEmptyResponseError as exc:
            self._client.record_invalid_response(type(exc).__name__)
            self.last_request_id = exc.request_id or ""
            self.last_usage = exc.token_usage
            self.last_finish_reason = exc.finish_reason
            raise
        except Exception as exc:
            self._client.record_invalid_response(type(exc).__name__)
            raise

    def suggest_palette(
        self, model: str, image_bytes: bytes, panel_context: str, **kwargs: Any
    ) -> PaletteSuggestion:
        """Analyze line art and context to suggest structured colour palette."""
        target_model = model or self.default_model
        validate_model_id_tier(target_model)
        b64_image = base64.b64encode(image_bytes).decode("ascii")
        media_type = detect_image_media_type(image_bytes)

        user = kwargs.pop("hashed_user_id", None) or kwargs.pop("user", None)

        call_reasoning_effort = kwargs.pop("reasoning_effort", self._reasoning_effort)
        call_reasoning_effort = validate_reasoning_effort(
            call_reasoning_effort, source="reasoning_effort kwarg"
        )
        call_reserve = kwargs.pop("reasoning_reserve_tokens", self._reasoning_reserve)
        if isinstance(call_reserve, bool) or not isinstance(call_reserve, int) or call_reserve < 0:
            raise MetaConfigurationError("reasoning_reserve_tokens must be a non-negative integer")

        instruction = (
            "Analyze this comic panel line art and context. Suggest a cohesive, harmonious "
            "color palette for the key visual regions. Respond strictly in valid JSON with this format:\n"
            '{\n  "theme": "Descriptive Theme Name",\n  "palette": [\n'
            '    {"region_name": "character_clothing", "hex_color": "#RRGGBB", "label": "Region Label"}\n'
            "  ]\n}\n"
            f"Panel context: {panel_context}"
        )

        content_parts = [
            {"type": "text", "text": instruction},
            {
                "type": "image_url",
                "image_url": {"url": f"data:{media_type};base64,{b64_image}"},
            },
        ]
        messages = [{"role": "user", "content": content_parts}]

        payload = build_chat_completion_request(
            model=target_model,
            messages=messages,
            user=user,
            user_field=self.user_field,
            reasoning_effort=call_reasoning_effort,
            reserve_tokens=call_reserve,
            **kwargs,
        )

        try:
            data, headers = self._client.post("/chat/completions", payload)
            self.last_status_code = 200
            self.last_response_keys = sorted(data.keys()) if isinstance(data, dict) else []
            content, token_usage, request_id, _ = parse_chat_completion_response(data, headers)

            self.last_request_id = request_id
            self.last_usage = token_usage
            self.last_finish_reason = token_usage.finish_reason
            return parse_palette_json(content)
        except MetaEmptyResponseError as exc:
            self._client.record_invalid_response(type(exc).__name__)
            self.last_request_id = exc.request_id or ""
            self.last_usage = exc.token_usage
            self.last_finish_reason = exc.finish_reason
            raise
        except Exception as exc:
            self._client.record_invalid_response(type(exc).__name__)
            raise
