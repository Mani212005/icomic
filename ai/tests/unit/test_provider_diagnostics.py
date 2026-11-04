"""Only bounded, redacted provider bodies may enter diagnostic artifacts."""

import json
import stat

import httpx
import pytest

from app.adapters.meta_http import MetaAPIError, MetaHttpClient, MetaTransientError
from app.provider_diagnostics import MAX_BODY_BYTES, ProviderDiagnostics


@pytest.mark.parametrize("status", [200, 400, 404, 500])
def test_failed_raw_bodies_are_complete_redacted_and_header_free(tmp_path, status):
    secret = "configured-unit-secret"
    content = {
        "error": {
            "message": "detail beyond old truncation " + "x" * 500 + " sk-response-secret " + secret
        },
        "api_key": "arbitrary-body-credential",
        "body_tail": "retained",
    }
    raw = (
        json.dumps(content).encode()
        if status != 200
        else b"not-json " + json.dumps(content).encode()
    )
    diagnostics = ProviderDiagnostics(tmp_path / "evidence")
    client = MetaHttpClient(
        api_key=secret,
        base_url="http://mock.meta.internal/v1",
        diagnostics=diagnostics,
        max_retries=0,
        transport=httpx.MockTransport(
            lambda request: httpx.Response(
                status,
                content=raw,
                headers={
                    "Authorization": "Bearer sk-header-secret",
                    "X-Secret": "never-record-any-header",
                },
            )
        ),
    )
    with pytest.raises(MetaAPIError):
        client.post(
            "/chat/completions",
            {"messages": [{"role": "user", "content": "private-request-never-store"}]},
        )
    files = list((tmp_path / "evidence").glob("*.json"))
    assert len(files) == 1
    text = files[0].read_text()
    for forbidden in [
        secret,
        "sk-response-secret",
        "arbitrary-body-credential",
        "sk-header-secret",
        "never-record-any-header",
        "private-request-never-store",
    ]:
        assert forbidden not in text
    body = json.loads(text)["body"]
    assert "x" * 500 in body and "retained" in body
    assert stat.S_IMODE(files[0].stat().st_mode) == 0o600
    assert files[0].stat().st_uid == files[0].parent.stat().st_uid
    assert not json.loads(text)["body_truncated"]


def test_redaction_precedes_utf8_safe_body_cap(tmp_path):
    diagnostics = ProviderDiagnostics(tmp_path)
    raw = ("sk-body-secret " + "é" * MAX_BODY_BYTES + " sk-tail-secret").encode()
    diagnostics.capture(raw, 200)
    diagnostics.failure("SchemaMismatch")
    item = json.loads(next(tmp_path.glob("*.json")).read_text())
    assert len(item["body"].encode()) <= MAX_BODY_BYTES
    assert item["body_truncated"]
    assert "sk-body-secret" not in item["body"]
    assert "�" not in item["body"]


def test_network_failure_never_records_a_previous_response_as_its_body(tmp_path):
    diagnostics = ProviderDiagnostics(tmp_path)
    calls = []

    def respond(request):
        calls.append(request)
        if len(calls) > 1:
            raise httpx.ConnectError("synthetic failure")
        return httpx.Response(200, json={"previous": "successful body"})

    client = MetaHttpClient(
        api_key="hermetic-replay",
        base_url="http://mock.meta.internal/v1",
        transport=httpx.MockTransport(respond),
        diagnostics=diagnostics,
        max_retries=0,
    )
    client.post("/chat/completions", {})
    with pytest.raises(MetaTransientError):
        client.post("/chat/completions", {})
    assert diagnostics.failure("MetaTransientError") is None
    assert not list(tmp_path.glob("*.json"))
