"""Exercise service model selection through Meta adapters with mocked HTTP only."""

import json

import httpx
import pytest

from app.adapters.language.meta import MetaLanguageAdapter
from app.adapters.vision.meta import MetaVisionAdapter
from app.services.colour import ColourService
from app.services.moderation import ModerationService
from app.services.panel import PanelService
from app.services.script import ScriptService


@pytest.mark.parametrize("operation", ["moderation", "script", "outline", "panels", "palette"])
@pytest.mark.parametrize("configured_model", [None, "configured-model"])
@pytest.mark.parametrize("override", [None, "explicit-model"])
def test_services_honor_adapter_model_selection(monkeypatch, operation, configured_model, override):
    for name in ("META_LANGUAGE_MODEL", "META_VISION_MODEL"):
        if configured_model:
            monkeypatch.setenv(name, configured_model)
        else:
            monkeypatch.delenv(name, raising=False)

    expected_model = override or configured_model or "muse-spark-1.3"
    requests = []

    def respond(request):
        payload = json.loads(request.content)
        requests.append(payload)
        if payload["model"] != expected_model:
            return httpx.Response(404, json={"error": {"message": "Unknown model"}})
        content = '{"theme":"Ocean","palette":[]}' if operation == "palette" else "Safe story"
        return httpx.Response(
            200,
            json={
                "choices": [{"message": {"content": content}, "finish_reason": "stop"}],
                "usage": {"prompt_tokens": 20, "completion_tokens": 10, "total_tokens": 30},
            },
        )

    adapter_type = MetaVisionAdapter if operation == "palette" else MetaLanguageAdapter
    adapter = adapter_type(
        api_key="mock-key-only",
        base_url="http://mock.meta.internal/v1",
        transport=httpx.MockTransport(respond),
    )
    kwargs = {"model_id": override} if override else {}
    if operation == "moderation":
        assert ModerationService(adapter, **kwargs).moderate_prompt("A bird flies home.") == (
            False,
            None,
        )
    elif operation == "script":
        ScriptService(adapter, **kwargs).analyze_script("A bird flies home.")
    elif operation == "outline":
        PanelService(adapter, **kwargs).generate_chapter_outline("A bird flies home.", 1, [])
    elif operation == "panels":
        PanelService(adapter, **kwargs).generate_panel_text(1, "Home", "A bird returns.", [])
    else:
        ColourService(adapter, **kwargs).suggest_palette(b"\x89PNG\r\n\x1a\n", "A bird")

    assert len(requests) == 1
    assert requests[0]["model"] == expected_model
