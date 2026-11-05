"""Only bounded, redacted provider bodies may enter diagnostic artifacts."""

import gzip
import json
import stat

import httpx
import pytest

from app.adapters.meta_http import (
    MetaAPIError,
    MetaHttpClient,
    MetaTransientError,
    MetaUnknownOutcomeError,
)
from app.provider_costs import ProviderCostLedger
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


@pytest.mark.parametrize("received_bytes", [0, 11995, MAX_BODY_BYTES + 100])
def test_incomplete_body_saves_only_safe_metadata_before_propagating(tmp_path, received_bytes):
    secret = "configured-unit-secret"
    payload = (b"private-partial-image-bytes " + secret.encode()).ljust(received_bytes, b"x")
    payload = payload[:received_bytes]

    class DisconnectedStream(httpx.SyncByteStream):
        closed = False

        def __iter__(self):
            if payload:
                yield payload
            raise httpx.RemoteProtocolError("incomplete image response")

        def close(self):
            self.closed = True

    stream = DisconnectedStream()
    requests = []

    def respond(request):
        requests.append(request)
        return httpx.Response(
            200,
            stream=stream,
            headers={
                "content-type": "application/json",
                "content-length": "1315453",
                "x-request-id": secret,
                "Authorization": "Bearer sk-header-secret",
                "Set-Cookie": "private-cookie",
                "X-Secret": "private-header",
            },
        )

    ledger = ProviderCostLedger(2)
    client = MetaHttpClient(
        api_key=secret,
        call_type="image",
        cost_stage="image",
        cost_ledger=ledger,
        diagnostics=ProviderDiagnostics(tmp_path),
        transport=httpx.MockTransport(respond),
    )
    with pytest.raises(MetaUnknownOutcomeError):
        client.post(
            "/responses",
            {
                "model": "muse-image-1.0",
                "input": [
                    {"content": [{"type": "input_text", "text": "private-request-never-store"}]}
                ],
            },
        )
    assert len(requests) == 1 and stream.closed
    files = list(tmp_path.glob("*.json"))
    assert len(files) == 1
    text = files[0].read_text()
    for forbidden in (
        secret,
        "private-partial-image-bytes",
        "sk-header-secret",
        "private-cookie",
        "private-header",
        "private-request-never-store",
    ):
        assert forbidden not in text
    record = json.loads(text)
    assert record["body_complete"] is False and record["body_saved"] is False
    assert "body" not in record
    assert record["received_bytes"] == received_bytes
    assert record["expected_bytes"] == 1315453
    assert record["first_bytes_length"] == min(received_bytes, 256)
    assert record["elapsed_seconds"] >= 0
    assert record["response_headers"] == {
        "content-type": "application/json",
        "content-length": "1315453",
        "x-request-id": "[REDACTED]",
    }
    assert stat.S_IMODE(files[0].stat().st_mode) == 0o600
    call = ledger.report()["calls"][0]
    assert call["status"] == "uncertain" and call["actual_cost_usd"] is None
    assert ledger.report()["accounted_spend_usd"] == 0.01


def test_complete_compressed_stream_is_decoded_once_and_retains_headers(tmp_path):
    encoded = gzip.compress(b'{"ok": true}')

    class CompressedStream(httpx.SyncByteStream):
        def __iter__(self):
            yield encoded[:10]
            yield encoded[10:]

    with MetaHttpClient(
        api_key="hermetic-replay",
        diagnostics=ProviderDiagnostics(tmp_path),
        transport=httpx.MockTransport(
            lambda request: httpx.Response(
                200,
                stream=CompressedStream(),
                headers={"content-encoding": "gzip"},
            )
        ),
    ) as client:
        data, headers = client.post("/chat/completions", {})
    assert data == {"ok": True}
    assert headers["content-encoding"] == "gzip"
    assert not list(tmp_path.glob("*.json"))
