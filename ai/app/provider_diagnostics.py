"""Opt-in failed-response evidence. Never store requests or credential headers."""

import json
import os
import re
import uuid
from collections.abc import Mapping
from contextvars import ContextVar
from pathlib import Path

from .provider_costs import provider_operation

MAX_BODY_BYTES = 256 * 1024
last_response = ContextVar("provider_diagnostic_response", default=None)


def redact_body(body: bytes, secret: str = "") -> str:
    text = body.decode("utf-8", errors="replace")
    if secret:
        # Replace both literal and JSON-escaped spellings before truncation.
        text = text.replace(secret, "[REDACTED]")
        text = text.replace(json.dumps(secret)[1:-1], "[REDACTED]")
    text = re.sub(r"(?i)\bBearer\s+[^\s\"'<>]+", "Bearer [REDACTED]", text)
    text = re.sub(r"\b(?:sk[-_]|meta_live_sk_)[A-Za-z0-9_-]+", "[REDACTED]", text)
    text = re.sub(
        r'(?i)("(?:authorization|api[_-]?key|access_token|secret|token)"\s*:\s*)"(?:\\.|[^"\\])*"',
        r'\1"[REDACTED]"',
        text,
    )
    return text


class ProviderDiagnostics:
    def __init__(self, directory: Path):
        self.directory = directory

    def capture(
        self, body: bytes, status: int, secret: str = "", phase: str = "generation"
    ) -> None:
        redacted = redact_body(body, secret).encode()
        last_response.set(
            {
                "http_status": status,
                "phase": phase,
                "body": redacted[:MAX_BODY_BYTES].decode("utf-8", errors="ignore"),
                "body_truncated": len(redacted) > MAX_BODY_BYTES,
                "saved": False,
            }
        )

    def capture_partial(
        self,
        *,
        status: int,
        received_bytes: int,
        expected_bytes: int | None,
        elapsed_seconds: float,
        headers: Mapping[str, str],
        first_bytes_length: int,
        secret: str = "",
    ) -> None:
        """Retain interruption metadata, not partial image/JSON content.

        Only response framing and correlation headers are permitted. Cookies,
        authorization, arbitrary headers and raw prefix bytes are excluded.
        """
        safe_headers = {}
        for name in ("content-type", "content-length", "content-encoding", "date", "x-request-id"):
            value = headers.get(name)
            if value is not None:
                safe_headers[name] = redact_body(value.encode(), secret)[:1024]
        last_response.set(
            {
                "http_status": status,
                "phase": "generation",
                "body_complete": False,
                "body_saved": False,
                "received_bytes": received_bytes,
                "expected_bytes": expected_bytes,
                "elapsed_seconds": round(elapsed_seconds, 6),
                "response_headers": safe_headers,
                "first_bytes_length": min(first_bytes_length, 256),
                "saved": False,
            }
        )

    def failure(self, error_type: str) -> str | None:
        response = last_response.get()
        if response is None or response["saved"]:
            return None
        operation, job_id = provider_operation.get()
        record = {k: v for k, v in response.items() if k != "saved"}
        record.update(operation=operation, job_id=job_id, error_type=error_type)
        self.directory.mkdir(parents=True, exist_ok=True, mode=0o700)
        path = self.directory / f"response-{uuid.uuid4().hex}.json"
        with os.fdopen(os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600), "w") as out:
            json.dump(record, out, ensure_ascii=False, indent=2)
            out.write("\n")
        # The runner creates the directory on the host before mounting it. A
        # root container must leave its private evidence readable by that owner.
        owner = self.directory.stat()
        written = path.stat()
        if (written.st_uid, written.st_gid) != (owner.st_uid, owner.st_gid):
            os.chown(path, owner.st_uid, owner.st_gid)
        response["saved"] = True
        return str(path)
