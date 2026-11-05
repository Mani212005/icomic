"""Image upload regression: virtual slow writes, bounded conditioning and cast selection."""

import hashlib
import io
import json
import random
from pathlib import Path

import httpx
import pytest
from PIL import Image

from app.adapters.image.conditioning import (
    ConditioningLimits,
    ImagePayloadError,
    conditioning_copy,
)
from app.adapters.image.meta import MetaImageAdapter
from app.adapters.meta_http import MetaConfigurationError, MetaHttpClient, MetaUnknownOutcomeError
from app.adapters.storage.fake import FakeStorageAdapter
from app.gen.models import CharacterRenderIdentity, PanelTextItem
from app.provider_costs import ProviderCostLedger
from app.services.render_brief import build_page_render_request
from tests.image_fixtures import image_bytes


def test_image_upload_has_independent_timeout_budgets_and_payload_telemetry():
    """A fake 90-second upload fits the image budget without real sleeps/network."""
    requests = []
    fixture = json.loads(
        (Path(__file__).parents[1] / "fixtures/meta/responses_image.json").read_text()
    )

    def slow_write(request):
        requests.append(request)
        budget = request.extensions["timeout"]
        assert budget == {"connect": 10, "write": 120, "read": 300, "pool": 10}
        if budget["write"] < 90:
            raise httpx.WriteTimeout("simulated 90-second upload", request=request)
        return httpx.Response(200, json=fixture)

    ledger = ProviderCostLedger(2)
    adapter = MetaImageAdapter(
        api_key="mock-key",
        transport=httpx.MockTransport(slow_write),
        cost_ledger=ledger,
        cost_stage="image",
    )
    source = image_bytes("Nia")
    adapter.generate("Nia at the lighthouse", [source], "user-hash", "storybook")
    assert len(requests) == 1
    call = ledger.report()["calls"][0]
    assert call["request_payload_bytes"] == len(requests[0].content)
    assert call["attached_image_bytes"] == len(source)
    assert call["attached_images"] == [
        {
            "sha256": hashlib.sha256(source).hexdigest(),
            "width": 64,
            "height": 64,
            "bytes": len(source),
        }
    ]
    assert call["elapsed_seconds"] >= 0
    assert call["actual_cost_usd"] == 0.01


@pytest.mark.parametrize("phase", ["WriteTimeout", "ReadTimeout"])
def test_image_timeout_keeps_reservation_without_retry(phase):
    requests = []

    def timeout(request):
        requests.append(request)
        raise getattr(httpx, phase)("simulated uncertain image POST", request=request)

    ledger = ProviderCostLedger(2)
    adapter = MetaImageAdapter(
        api_key="mock-key",
        transport=httpx.MockTransport(timeout),
        cost_ledger=ledger,
        cost_stage="image",
    )
    with pytest.raises(MetaUnknownOutcomeError, match="Do not retry automatically"):
        adapter.generate("A lighthouse", [], "user-hash", "storybook")
    assert len(requests) == 1
    report = ledger.report()
    assert report["actual_spend_usd"] == 0
    assert report["accounted_spend_usd"] == 0.01
    assert report["unpriced_calls"] == 1
    assert report["calls"][0]["http_status"] is None
    assert report["calls"][0]["request_payload_bytes"] > 0


def test_text_timeouts_are_shorter_and_image_timeouts_configurable(monkeypatch):
    captured = []

    def respond(request):
        captured.append(request.extensions["timeout"])
        return httpx.Response(200, json={})

    with MetaHttpClient(api_key="mock-key", transport=httpx.MockTransport(respond)) as client:
        client.post("/chat/completions", {"model": "test", "messages": []})
    assert captured.pop() == {"connect": 10, "write": 30, "read": 120, "pool": 10}
    monkeypatch.setenv("META_IMAGE_WRITE_TIMEOUT_SECONDS", "180")
    monkeypatch.setenv("META_IMAGE_READ_TIMEOUT_SECONDS", "420")
    with MetaHttpClient(
        api_key="mock-key", transport=httpx.MockTransport(respond), call_type="image"
    ) as client:
        client.post("/responses", {"model": "test", "input": []})
    assert captured.pop() == {"connect": 10, "write": 180, "read": 420, "pool": 10}


@pytest.mark.parametrize("value", ["0", "-1", "nan", "inf", "invalid"])
def test_invalid_timeout_configuration_refuses_before_http(monkeypatch, value):
    monkeypatch.setenv("META_IMAGE_WRITE_TIMEOUT_SECONDS", value)
    with pytest.raises((MetaConfigurationError, RuntimeError), match="positive finite"):
        MetaImageAdapter(api_key="mock-key")


@pytest.mark.parametrize("limit", ["REFERENCE", "TOTAL_REFERENCE", "REQUEST"])
def test_payload_limits_refuse_before_transmission_or_reservation(monkeypatch, limit):
    monkeypatch.setenv(f"META_IMAGE_MAX_{limit}_BYTES", "1")
    requests = []
    ledger = ProviderCostLedger(2)
    adapter = MetaImageAdapter(
        api_key="mock-key",
        cost_ledger=ledger,
        cost_stage="image",
        transport=httpx.MockTransport(lambda request: requests.append(request)),
    )
    with pytest.raises(ImagePayloadError, match="limit"):
        adapter.generate("Nia at sea", [image_bytes("Nia")], "user-hash", "storybook")
    assert requests == []
    assert ledger.report()["calls"] == []


def test_conditioning_preserves_original_and_bounds_noisy_image():
    source_image = Image.frombytes("RGB", (1920, 1280), random.Random(0).randbytes(1920 * 1280 * 3))
    output = io.BytesIO()
    source_image.save(output, format="PNG")
    original = output.getvalue()
    before = hashlib.sha256(original).hexdigest()
    limits = ConditioningLimits()
    derived, metadata = conditioning_copy(original, limits)
    assert hashlib.sha256(original).hexdigest() == before
    assert metadata["source_sha256"] == before
    assert metadata["sha256"] == hashlib.sha256(derived).hexdigest()
    assert len(derived) <= limits.image_bytes
    assert max(metadata["width"], metadata["height"]) <= 1024
    assert abs(metadata["width"] / metadata["height"] - 1.5) < 0.01
    with Image.open(io.BytesIO(derived)) as image:
        assert image.format == "WEBP"
    with pytest.raises(ImagePayloadError, match="minimum resolution"):
        conditioning_copy(original, ConditioningLimits(image_bytes=1))


def test_page_cast_selection_and_composition_keep_order_and_original_assets():
    storage = FakeStorageAdapter()
    names = ["Nia", "Arun", "Mira"]
    originals = {name: image_bytes(name) for name in names}
    urls = {
        name: storage.put(name + ".webp", content, "image/webp")
        for name, content in originals.items()
    }
    composition = image_bytes("composition")
    draft = storage.put("draft.webp", composition, "image/webp")
    identities = [
        CharacterRenderIdentity(
            character_id=name,
            name=name,
            description=name,
            visual_attributes={},
            reference_image_url=urls[name],
        )
        for name in names
    ]
    panels = [
        PanelTextItem(
            panel_number=1,
            scene_description="Nia and ARUN at sea",
            character_actions="Watches",
            camera_angle="Wide",
            background="Sea",
            image_prompt="Storm",
            dialogue="Mira is at home.",
            narration="Mira remembers.",
        )
    ]
    prompt, images = build_page_render_request(
        page_number=1,
        panels=panels,
        style="storybook",
        storage=storage,
        character_identities=identities,
        character_reference_image_urls=list(urls.values()),
        draft_image_url=draft,
    )
    brief = json.JSONDecoder().raw_decode(prompt[prompt.index("{") :])[0]
    assert images == [composition, originals["Nia"], originals["Arun"]]
    assert [ref.get("name") for ref in brief["references"]] == [None, "Nia", "Arun"]
    for name in names:
        assert storage.get(urls[name]) == originals[name]
    panels[0] = panels[0].model_copy(update={"scene_description": "A mania for clocks"})
    _, images = build_page_render_request(
        page_number=2,
        panels=panels,
        style="storybook",
        storage=storage,
        character_identities=identities,
    )
    assert images == []
