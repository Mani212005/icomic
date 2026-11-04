"""Exercise every comic service using strict, hermetic Meta HTTP responses."""

import base64
import json
import struct
import zlib
from io import BytesIO
from uuid import uuid4

import httpx
from fastapi.testclient import TestClient
from PIL import Image

from app.adapters.factory import build_adapters_from_env
from app.adapters.image.meta import MetaImageAdapter
from app.adapters.language.fake import FakeLanguageAdapter
from app.adapters.language.meta import MetaLanguageAdapter
from app.adapters.language.port import Message
from app.adapters.vision.meta import MetaVisionAdapter
from app.main import create_app
from app.provider_costs import ProviderCostLedger


def webp_with_provenance():
    output = BytesIO()
    Image.new("RGB", (3, 2), (20, 60, 90)).save(output, "WEBP", lossless=True)
    data = output.getvalue() + b"JUMB" + struct.pack("<I", 4) + b"c2pa"
    return data[:4] + struct.pack("<I", len(data) - 8) + data[8:]


def test_entire_meta_service_flow_with_webp_and_moderation_costs(tmp_path):
    webp = webp_with_provenance()
    calls = []

    def respond(request):
        if request.url.path.endswith("/responses/input_tokens"):
            return httpx.Response(200, json={"input_tokens": 100})
        payload = json.loads(request.content)
        calls.append((request.url.path, payload))
        if request.url.path.endswith("/chat/completions"):
            assert any(m["role"] in {"user", "tool"} for m in payload["messages"])
            assert payload["max_tokens"] >= 1024 + 32
            assert payload["reasoning_effort"] == "low"
            content = payload["messages"][-1]["content"]
            if isinstance(content, list):
                url = content[1]["image_url"]["url"]
                assert url == "data:image/webp;base64," + base64.b64encode(webp).decode()
                answer = '{"theme":"Harbour","palette":[{"region_name":"sea","hex_color":"#143C5A","label":"Sea"}]}'
            else:
                answer = FakeLanguageAdapter().complete(
                    "fake",
                    [Message(role=m["role"], content=m["content"]) for m in payload["messages"]],
                )
            return httpx.Response(
                200,
                json={
                    "choices": [{"message": {"content": answer}, "finish_reason": "stop"}],
                    "usage": {
                        "prompt_tokens": 100,
                        "completion_tokens": 50,
                        "completion_tokens_details": {"reasoning_tokens": 20},
                    },
                },
            )
        assert request.url.path.endswith("/responses")
        assert payload["input"][0]["role"] == "user"
        for part in payload["input"][0]["content"]:
            if part["type"] == "input_image":
                assert (
                    part["image_url"] == "data:image/webp;base64," + base64.b64encode(webp).decode()
                )
        return httpx.Response(
            200,
            json={
                "output": [
                    {"type": "image_generation_call", "result": base64.b64encode(webp).decode()}
                ]
            },
        )

    ledger = ProviderCostLedger(
        2, prior="0.017006", reserve="0.0196245", path=tmp_path / "costs.json"
    )
    kwargs = {
        "api_key": "mock-key",
        "base_url": "http://mock.meta.internal/v1",
        "transport": httpx.MockTransport(respond),
        "cost_ledger": ledger,
    }
    adapters = build_adapters_from_env()
    adapters.language = MetaLanguageAdapter(**kwargs, cost_stage="language")
    adapters.vision = MetaVisionAdapter(**kwargs, cost_stage="vision")
    adapters.image = MetaImageAdapter(**kwargs, cost_stage="image")
    adapters.cost_ledger = ledger
    app = create_app(adapters)
    client = TestClient(app)
    project, character, chapter, page = [str(uuid4()) for _ in range(4)]
    common = {"hashed_user_id": "hashed-user", "project_id": project}

    def post(operation, **payload):
        request = {**common, "job_id": str(uuid4()), **payload}
        response = client.post("/internal/v1/" + operation, json=request)
        assert response.status_code == 200, response.text
        return response.json(), request

    moderation, req = post("moderate-prompt", prompt="A bird returns home.")
    status = client.get(f"/internal/v1/job/{req['job_id']}/status").json()
    assert status["actual_cost"] == 0.0003375
    # Redelivery does not invoke or bill the provider again.
    before = len(calls)
    assert client.post("/internal/v1/moderate-prompt", json=req).json() == moderation
    assert len(calls) == before
    blocked, blocked_req = post("moderate-prompt", prompt="nsfw")
    assert blocked["blocked"] is True
    assert client.get(f"/internal/v1/job/{blocked_req['job_id']}/status").json()["actual_cost"] == 0

    analysis, _ = post("analyze-script", script="A bird returns home.", target_chapter_count=1)
    reference, _ = post(
        "generate-character-reference",
        character_id=character,
        character_name="Nia",
        prompt="An explorer",
        style="storybook",
    )
    outline, _ = post(
        "generate-chapter-outline",
        script="A bird returns home.",
        chapter_count=1,
        approved_characters=analysis["character_profiles"],
    )
    panels, _ = post(
        "generate-panel-text",
        chapter_id=chapter,
        chapter_number=1,
        chapter_title=outline["chapters"][0]["title"],
        chapter_summary="A safe return",
        characters=["Nia"],
    )
    draft, _ = post(
        "generate-draft-sketch",
        page_id=page,
        page_number=1,
        panels=panels["panels"],
        style="storybook",
        character_reference_image_urls=[reference["image_url"]],
    )
    ink, _ = post(
        "generate-line-art",
        page_id=page,
        page_number=1,
        draft_image_url=draft["image_url"],
        panels=panels["panels"],
        style="storybook",
        character_reference_image_urls=[reference["image_url"]],
    )
    for result in (reference, draft, ink):
        assert result["image_url"].endswith(".webp")
        assert adapters.storage.get(result["image_url"]) == webp
        assert adapters.storage.get_content_type(result["image_url"]) == "image/webp"
    assert len([p for p in calls[-1][1]["input"][0]["content"] if p["type"] == "input_image"]) == 2
    palette, _ = post(
        "suggest-colour-palette",
        page_id=page,
        line_art_image_url=ink["image_url"],
        panel_context="Harbour",
    )
    assert palette["palette"][0]["hex_color"] == "#143C5A"
    exported, _ = post(
        "assemble-pdf",
        title="Bird & Sea",
        page_size="A4",
        include_ai_disclosure=True,
        pages=[
            {
                "page_number": 1,
                "image_url": ink["image_url"],
                "provenance_metadata": ink["provenance_metadata"],
            }
        ],
    )
    pdf = adapters.storage.get(exported["pdf_url"])
    assert pdf.startswith(b"%PDF-")
    assert exported["page_count"] == 2
    assert b"/EmbeddedFiles" in pdf and webp in pdf
    assert b"Bird &amp; Sea" in pdf
    rgb = zlib.compress(bytes((20, 60, 90)) * 6)
    assert rgb in pdf
    report = ledger.report()
    assert len(report["calls"]) == 8
    assert report["unpriced_calls"] == 0
    assert abs(report["actual_spend_usd"] - (5 * 0.0003375 + 3 * 0.01)) < 1e-12
    saved = (tmp_path / "costs.json").read_text()
    assert "A bird" not in saved and "mock-key" not in saved


def test_multicall_script_job_reports_all_observed_provider_usage(tmp_path):
    def respond(request):
        if request.url.path.endswith("/responses/input_tokens"):
            return httpx.Response(200, json={"input_tokens": 100})
        payload = json.loads(request.content)
        answer = FakeLanguageAdapter().complete(
            "fake", [Message(role=m["role"], content=m["content"]) for m in payload["messages"]]
        )
        return httpx.Response(
            200,
            json={
                "choices": [{"message": {"content": answer}, "finish_reason": "stop"}],
                "usage": {"prompt_tokens": 100, "completion_tokens": 50},
            },
        )

    ledger = ProviderCostLedger(2, path=tmp_path / "costs.json")
    adapters = build_adapters_from_env()
    adapters.language = MetaLanguageAdapter(
        api_key="mock-key",
        base_url="http://mock.meta.internal/v1",
        transport=httpx.MockTransport(respond),
        cost_ledger=ledger,
    )
    adapters.cost_ledger = ledger
    client = TestClient(create_app(adapters))
    job_id = str(uuid4())
    response = client.post(
        "/internal/v1/analyze-script",
        json={
            "job_id": job_id,
            "project_id": str(uuid4()),
            "hashed_user_id": "hash",
            "script": "story " * 1001,
        },
    )
    assert response.status_code == 200, response.text
    status = client.get(f"/internal/v1/job/{job_id}/status").json()
    assert len(ledger.report()["calls"]) == 3
    assert abs(status["actual_cost"] - 3 * 0.0003375) < 1e-12
