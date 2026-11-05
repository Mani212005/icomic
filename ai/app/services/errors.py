"""Safe, stable error metadata used at the worker and HTTP boundaries."""

from dataclasses import dataclass
from math import isfinite

from fastapi import HTTPException

from ..adapters.meta_http import (
    MetaAPIError,
    MetaConfigurationError,
    MetaContentPolicyError,
    MetaEmptyResponseError,
    MetaRateLimitError,
    MetaTransientError,
    MetaUnknownOutcomeError,
)
from ..provider_output import ProviderOutputError


@dataclass(frozen=True)
class SafeError:
    code: str
    message: str
    status: int
    retryable: bool = False
    retry_after_seconds: float | None = None


class JobExecutionError(HTTPException):
    def __init__(self, error: SafeError):
        super().__init__(status_code=error.status, detail=error.message)
        self.error = error


def classify_error(exc: Exception) -> SafeError:
    if isinstance(exc, MetaContentPolicyError):
        return SafeError(
            "CONTENT_POLICY_REJECTED",
            "This request could not be completed under content guidelines. Edit it and try again.",
            400,
        )
    if isinstance(exc, MetaRateLimitError):
        retry_after = exc.retry_after
        if retry_after is not None and (not isfinite(retry_after) or retry_after < 0):
            retry_after = None
        return SafeError(
            "RATE_LIMITED",
            "Generation is temporarily rate limited. Try again later.",
            429,
            True,
            retry_after,
        )
    if isinstance(exc, MetaUnknownOutcomeError) or getattr(exc, "outcome_unknown", False):
        return SafeError(
            "OUTCOME_UNKNOWN",
            "The generation outcome could not be confirmed. Check its status before retrying.",
            502,
        )
    if isinstance(exc, MetaTransientError):
        return SafeError(
            "UPSTREAM_UNAVAILABLE",
            "Generation service is temporarily unavailable. Try again later.",
            503,
            True,
        )
    if isinstance(exc, MetaConfigurationError):
        return SafeError(
            "SERVICE_CONFIGURATION",
            "Generation service is not configured correctly. Contact support.",
            503,
        )
    if isinstance(exc, (MetaEmptyResponseError, ProviderOutputError)):
        return SafeError(
            "INVALID_PROVIDER_OUTPUT",
            "Generation service returned an unusable result. Try again later.",
            502,
        )
    if isinstance(exc, MetaAPIError):
        return SafeError(
            "UPSTREAM_ERROR",
            "Generation service could not complete this request. Try again later.",
            502,
        )
    if isinstance(exc, HTTPException):
        if exc.status_code == 409:
            return SafeError("CONFLICT", "This request conflicts with an existing job.", 409)
        if exc.status_code == 404:
            return SafeError("NOT_FOUND", "The requested resource was not found.", 404)
        if exc.status_code == 401:
            return SafeError("UNAUTHORIZED", "Authentication is required.", 401)
        if exc.status_code == 403:
            return SafeError("FORBIDDEN", "This request is not permitted.", 403)
        if exc.status_code == 429:
            return SafeError(
                "RATE_LIMITED",
                "Generation is temporarily rate limited. Try again later.",
                429,
                True,
            )
        if exc.status_code == 503:
            return SafeError(
                "SERVICE_UNAVAILABLE",
                "Generation service is temporarily unavailable. Try again later.",
                503,
                True,
            )
        if exc.status_code in (400, 422):
            return SafeError(
                "INVALID_ARGUMENT",
                "Review the request and try again.",
                exc.status_code,
            )
        if exc.status_code == 413:
            return SafeError("REQUEST_TOO_LARGE", "The request exceeds the allowed size.", 413)
    if isinstance(exc, ValueError):
        return SafeError("INVALID_ARGUMENT", "Review the request and try again.", 400)
    return SafeError(
        "INTERNAL_ERROR",
        "Generation could not be completed. Try again later.",
        500,
    )
