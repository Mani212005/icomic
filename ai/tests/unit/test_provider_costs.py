"""Prove the shared cap is enforced before HTTP, including failed and pending calls."""

import json
from concurrent.futures import ThreadPoolExecutor

import httpx
import pytest

from app.adapters.language.meta import MetaLanguageAdapter
from app.adapters.language.port import Message
from app.adapters.meta_http import (
    MetaAPIError,
    MetaConfigurationError,
    build_chat_completion_request,
    build_responses_image_request,
)
from app.provider_costs import ProviderAccountingError, ProviderBudgetError, ProviderCostLedger


def test_budget_includes_prior_spend_reserve_and_pending_calls_across_restart(tmp_path):
    path = tmp_path / "costs.json"
    ledger = ProviderCostLedger(2, prior="1.97", reserve="0.02", path=path)
    ledger.before_call("image", {"model": "muse-image-1.0"})
    restarted = ProviderCostLedger(2, prior="1.97", reserve="0.02", path=path)
    with pytest.raises(ProviderBudgetError):
        restarted.before_call("image", {"model": "muse-image-1.0"})
    report = restarted.report()
    assert report["accounted_spend_usd"] == 0.01
    assert report["actual_spend_usd"] == 0
    assert report["unpriced_calls"] == 1


def test_concurrent_guards_cannot_reserve_the_same_remaining_budget(tmp_path):
    path = tmp_path / "shared.json"
    ledgers = [ProviderCostLedger(2, prior="1.97", reserve="0.02", path=path) for _ in range(4)]

    def reserve(ledger):
        try:
            ledger.before_call("image", {"model": "muse-image-1.0"})
            return True
        except ProviderBudgetError:
            return False

    with ThreadPoolExecutor(4) as pool:
        assert sum(pool.map(reserve, ledgers)) == 1


@pytest.mark.parametrize("failure", ["empty", "http", "network", "missing_usage"])
def test_failures_are_accounted_before_parsing_without_retries(tmp_path, failure):
    calls = []

    def respond(request):
        if request.url.path.endswith("/responses/input_tokens"):
            return httpx.Response(200, json={"input_tokens": 20})
        calls.append(request)
        if failure == "network":
            raise httpx.ReadTimeout("Mock timeout")
        if failure == "http":
            return httpx.Response(400, json={"error": {"type": "invalid_request_error"}})
        result = {
            "choices": [
                {
                    "message": {"content": "" if failure == "empty" else "Safe"},
                    "finish_reason": "length",
                }
            ]
        }
        if failure != "missing_usage":
            result["usage"] = {
                "prompt_tokens": 20,
                "completion_tokens": 1056,
                "completion_tokens_details": {"reasoning_tokens": 1056},
            }
        return httpx.Response(200, json=result)

    ledger = ProviderCostLedger(2, reserve="0.0196245", path=tmp_path / "costs.json")
    adapter = MetaLanguageAdapter(
        api_key="mock-key",
        base_url="http://mock.meta.internal/v1",
        transport=httpx.MockTransport(respond),
        cost_ledger=ledger,
    )
    with pytest.raises((MetaAPIError, ProviderAccountingError)):
        adapter.complete("", [Message(role="user", content="Safe")], max_tokens=32)
    assert len(calls) == 1
    report = ledger.report()
    assert len(report["calls"]) == 1
    if failure == "empty":
        assert report["actual_spend_usd"] == (20 * 1.25 + 1056 * 4.25) / 1_000_000
        assert report["unpriced_calls"] == 0
    else:
        assert report["unpriced_calls"] == 1
        assert report["accounted_spend_usd"] > 0
    assert json.loads((tmp_path / "costs.json").read_text())["calls"]


def test_guard_refuses_before_http_and_requires_wire_output_bound():
    ledger = ProviderCostLedger(2, prior="1.97", reserve="0.029")
    calls = []
    adapter = MetaLanguageAdapter(
        api_key="mock-key",
        base_url="http://mock.meta.internal/v1",
        transport=httpx.MockTransport(
            lambda request: (
                httpx.Response(200, json={"input_tokens": 20})
                if request.url.path.endswith("/responses/input_tokens")
                else calls.append(request)
            )
        ),
        cost_ledger=ledger,
    )
    with pytest.raises(ProviderBudgetError):
        adapter.complete("", [Message(role="user", content="Safe")], max_tokens=32)
    with pytest.raises(ProviderBudgetError, match="max_tokens"):
        adapter.complete("", [Message(role="user", content="Safe")])
    assert not calls


@pytest.mark.parametrize(
    "messages",
    [
        [],
        [{"role": "system", "content": "Only system"}],
        [{"role": "user", "content": ""}],
        [{"role": "unknown", "content": "Hi"}, {"role": "user", "content": "Hi"}],
    ],
)
def test_chat_builder_rejects_invalid_messages_centrally(messages):
    with pytest.raises(MetaConfigurationError):
        build_chat_completion_request("model", messages)


def test_image_builder_rejects_missing_prompt_and_text_disguised_as_reference():
    with pytest.raises(MetaConfigurationError):
        build_responses_image_request("model", " ")
    with pytest.raises(MetaConfigurationError):
        build_responses_image_request(
            "model", "A bird", reference_images=[b"https://storage.invalid/image.png"]
        )


@pytest.mark.parametrize(
    "kwargs",
    [{"stream": True}, {"n": 2}, {"tools": [{"type": "web_search"}]}, {"truncation": "auto"}],
)
def test_chat_builder_rejects_unimplemented_or_unbudgeted_parameters(kwargs):
    with pytest.raises(MetaConfigurationError):
        build_chat_completion_request("model", [Message(role="user", content="Hi")], **kwargs)


def test_images_cannot_be_put_in_system_messages_or_mislabeled():
    image = {"type": "image_url", "image_url": {"url": "data:image/png;base64,UkZG"}}
    with pytest.raises(MetaConfigurationError, match="user role"):
        build_chat_completion_request(
            "model", [{"role": "system", "content": [image]}, {"role": "user", "content": "Hi"}]
        )
    with pytest.raises(MetaConfigurationError, match="media type"):
        build_chat_completion_request("model", [{"role": "user", "content": [image]}])


def test_cached_input_usage_is_discounted_without_double_counting_reasoning(tmp_path):
    ledger = ProviderCostLedger(2, path=tmp_path / "cached.json")
    index = ledger.before_call("vision", {"model": "muse-spark-1.3", "max_tokens": 1088})
    ledger.finish_call(
        index,
        200,
        {
            "usage": {
                "prompt_tokens": 1000,
                "completion_tokens": 80,
                "prompt_tokens_details": {"cached_tokens": 800},
                "completion_tokens_details": {"reasoning_tokens": 60},
            }
        },
    )
    assert ledger.report()["actual_spend_usd"] == (200 * 1.25 + 800 * 0.15 + 80 * 4.25) / 1_000_000
    assert ledger.report()["calls"][0]["input_bound"] == 1_048_576


@pytest.mark.parametrize("stage", ["language", "vision"])
@pytest.mark.parametrize("inputs,outputs", [(1, 1056), (3271, 1088), (16384, 5120)])
def test_explicit_reservation_covers_maximum_uncached_usage(tmp_path, stage, inputs, outputs):
    ledger = ProviderCostLedger(2, path=tmp_path / "maximum.json")
    index = ledger.before_call(
        stage, {"model": "muse-spark-1.3", "max_tokens": outputs}, input_tokens=inputs
    )
    ledger.finish_call(
        index, 200, {"usage": {"prompt_tokens": inputs, "completion_tokens": outputs}}
    )
    call = ledger.report()["calls"][0]
    assert call["input_bound"] == inputs
    assert call["output_bound"] == outputs
    assert call["reserved_cost_usd"] == call["actual_cost_usd"]


def test_explicit_worst_case_exceeding_headroom_is_refused_before_generation():
    requests = []

    def respond(request):
        requests.append(request.url.path)
        assert request.url.path.endswith("/responses/input_tokens")
        return httpx.Response(200, json={"input_tokens": 3271})

    ledger = ProviderCostLedger(2, prior="1.98", reserve="0.015")
    adapter = MetaLanguageAdapter(
        api_key="mock-key",
        base_url="http://mock.meta.internal/v1",
        transport=httpx.MockTransport(respond),
        cost_ledger=ledger,
    )
    with pytest.raises(ProviderBudgetError, match="before transmission"):
        adapter.complete("", [Message(role="user", content="Story")], max_tokens=4096)
    assert requests == ["/v1/responses/input_tokens"]
    assert ledger.report()["calls"] == []


@pytest.mark.parametrize(
    "result",
    [
        None,
        {},
        {"input_tokens": True},
        {"input_tokens": -1},
        {"input_tokens": 1.2},
        {"input_tokens": 1_048_576},
    ],
)
def test_invalid_or_unavailable_count_never_reaches_generation(result):
    calls = []

    def respond(request):
        calls.append(request.url.path)
        return httpx.Response(503 if result is None else 200, json=result)

    ledger = ProviderCostLedger(2)
    adapter = MetaLanguageAdapter(
        api_key="mock-key",
        base_url="http://mock.meta.internal/v1",
        transport=httpx.MockTransport(respond),
        cost_ledger=ledger,
    )
    with pytest.raises(ProviderBudgetError):
        adapter.complete("", [Message(role="user", content="Story")], max_tokens=4096)
    assert calls == ["/v1/responses/input_tokens"]
    assert ledger.report()["calls"] == []


def test_failed_usage_cannot_release_a_reservation(tmp_path):
    ledger = ProviderCostLedger(2, path=tmp_path / "failed.json")
    index = ledger.before_call(
        "language", {"model": "muse-spark-1.3", "max_tokens": 1056}, input_tokens=100
    )
    ledger.finish_call(index, 500, {"usage": {"prompt_tokens": 1, "completion_tokens": 1}})
    report = ledger.report()
    assert report["unpriced_calls"] == 1
    assert report["actual_spend_usd"] == 0
    assert report["accounted_spend_usd"] == report["calls"][0]["reserved_cost_usd"]


def test_count_request_preserves_exact_text_media_and_model_without_generation(tmp_path):
    from app.adapters.vision.meta import MetaVisionAdapter

    # Use an inline PNG with real magic bytes, as accepted by the request builder.
    raw = b"\x89PNG\r\n\x1a\n" + b"mock-image"
    captured = []

    def respond(request):
        captured.append(json.loads(request.content))
        assert request.url.path.endswith("/responses/input_tokens")
        return httpx.Response(200, json={"input_tokens": 1_000_000})

    ledger = ProviderCostLedger(2, prior=1)
    adapter = MetaVisionAdapter(
        api_key="mock-key",
        base_url="http://mock.meta.internal/v1",
        transport=httpx.MockTransport(respond),
        cost_ledger=ledger,
        cost_stage="vision",
    )
    with pytest.raises(ProviderBudgetError):
        adapter.review("", raw, "Look at this café", max_tokens=64)
    import base64

    assert captured == [
        {
            "model": "muse-spark-1.3",
            "input": [
                {
                    "role": "user",
                    "content": [
                        {"type": "input_text", "text": "Look at this café"},
                        {
                            "type": "input_image",
                            "image_url": "data:image/png;base64," + base64.b64encode(raw).decode(),
                        },
                    ],
                }
            ],
        }
    ]
    assert ledger.report()["calls"] == []


@pytest.mark.parametrize("inputs,outputs", [(101, 32), (100, 33)])
def test_usage_exceeding_either_explicit_bound_stops_even_if_total_price_fits(inputs, outputs):
    ledger = ProviderCostLedger(2)
    index = ledger.before_call(
        "language", {"model": "muse-spark-1.3", "max_tokens": 32}, input_tokens=100
    )
    with pytest.raises(ProviderAccountingError, match="explicit token bounds"):
        ledger.finish_call(
            index, 200, {"usage": {"prompt_tokens": inputs, "completion_tokens": outputs}}
        )


@pytest.mark.parametrize(
    "stage,endpoint,extra",
    [
        ("language", "/chat/completions", {"n": 2}),
        ("language", "/chat/completions", {"tools": [{"type": "web_search"}]}),
        ("image", "/responses", {"n": 2}),
        ("image", "/responses", {"previous_response_id": "history"}),
    ],
)
def test_direct_capped_http_cannot_bypass_single_call_bounds(stage, endpoint, extra):
    from app.adapters.meta_http import MetaHttpClient

    calls = []
    client = MetaHttpClient(
        api_key="mock-key",
        base_url="http://mock.meta.internal/v1",
        transport=httpx.MockTransport(lambda request: calls.append(request)),
        cost_ledger=ProviderCostLedger(2),
        cost_stage=stage,
    )
    with pytest.raises(ProviderBudgetError):
        payload = (
            {"model": "muse-image-1.0", "input": [{"role": "user", "content": "Hi"}]}
            if stage == "image"
            else {
                "model": "muse-spark-1.3",
                "messages": [{"role": "user", "content": "Hi"}],
                "max_tokens": 32,
            }
        )
        client.post(endpoint, {**payload, **extra})
    assert calls == []
