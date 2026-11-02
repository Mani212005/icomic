"""Meta language adapter using OpenAI-compatible chat completions."""

from __future__ import annotations

import os
from typing import Any

import httpx

from ..meta_http import (
    DEFAULT_META_LANGUAGE_MODEL,
    DEFAULT_META_USER_FIELD,
    MetaConfigurationError,
    MetaEmptyResponseError,
    MetaHttpClient,
    build_chat_completion_request,
    parse_chat_completion_response,
    validate_model_id_tier,
)
from .port import LanguagePort, Message, TokenUsage


class MetaLanguageAdapter(LanguagePort):
    """Real Meta language model adapter for dialogue generation and script analysis."""

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
            default_model or os.getenv("META_LANGUAGE_MODEL") or DEFAULT_META_LANGUAGE_MODEL
        )
        validate_model_id_tier(self.default_model)
        self.user_field = os.getenv("META_USER_FIELD", DEFAULT_META_USER_FIELD)

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
                raise RuntimeError(f"MetaLanguageAdapter is not configured: {exc.message}") from exc

        self.last_request_id: str = ""
        self.last_usage: TokenUsage | None = None
        self.last_finish_reason: str | None = None
        self.last_cost_tokens: int = 0
        self.last_status_code: int | None = None
        self.last_response_keys: list[str] = []

    def complete(self, model: str, messages: list[Message], **kwargs: Any) -> str:
        """Execute text completion via Meta chat completions endpoint."""
        target_model = model or self.default_model
        validate_model_id_tier(target_model)

        # Forward hashed_user_id under the configured user field
        user = kwargs.pop("hashed_user_id", None) or kwargs.pop("user", None)

        payload = build_chat_completion_request(
            model=target_model,
            messages=messages,
            user=user,
            user_field=self.user_field,
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
            self.last_cost_tokens = token_usage.total_tokens

            return content
        except MetaEmptyResponseError as exc:
            self.last_request_id = exc.request_id or ""
            self.last_usage = exc.token_usage
            self.last_finish_reason = exc.finish_reason
            if exc.token_usage:
                self.last_cost_tokens = exc.token_usage.total_tokens
            raise

    def estimate_tokens(self, messages: list[Message]) -> TokenUsage:
        """Estimate token usage for cost estimation prior to generation."""
        chars = sum(len(m.content) for m in messages)
        est_tokens = max(1, chars // 4)
        return TokenUsage(
            input_tokens=est_tokens,
            output_tokens=50,
            total_tokens=est_tokens + 50,
        )
