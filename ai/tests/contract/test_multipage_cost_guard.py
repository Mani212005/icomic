"""Replay the long fixture's service calls with recorded provider usage, without network.

Inventory: docs/MULTIPAGE.md and https://github.com/Mani212005/icomic/pull/36.
Usage: data/icomic-night-d-live-run/result.md, attempt 3 (2026-11-04).
Use the exact recorded token pairs rather than rounded dollar estimates. Analysis
uses 500-word requests; its recorded short-request price is a replay
fixture for this affordability proof, not a prediction of future live charges.
"""

import base64
import importlib.util
import json
from collections import Counter
from io import BytesIO
from pathlib import Path

import httpx
import pytest
from PIL import Image

from app.adapters.image.meta import MetaImageAdapter
from app.adapters.language.fake import FakeLanguageAdapter
from app.adapters.language.meta import MetaLanguageAdapter
from app.adapters.language.port import Message
from app.adapters.storage.fake import FakeStorageAdapter
from app.adapters.vision.meta import MetaVisionAdapter
from app.costs import calculate_checklist_worst_case
from app.provider_costs import ProviderCostLedger
from app.services.character import CharacterService
from app.services.colour import ColourService
from app.services.draft import DraftService
from app.services.line_art import LineArtService
from app.services.moderation import ModerationService
from app.services.pacing import panel_count, split_text
from app.services.panel import PanelService
from app.services.script import ScriptService

# Exact recorded input/full-completion pairs. Reasoning is included in completion.
USAGE = {
    "moderation": (69, 737),
    "characters": (70, 678),
    "outline": (71, 762),
    "panels": (50, 1056),
    "vision": (3271, 809),
}
FIXTURE = Path(__file__).resolve().parents[3] / "tests/fixtures/the_tide_clock.txt"


@pytest.mark.parametrize("regenerations,replays", [(0, 0), (2, 1)])
def test_long_fixture_with_recorded_costs_completes_under_shared_cap(
    tmp_path, capsys, regenerations, replays
):
    buffer = BytesIO()
    Image.new("RGB", (1920, 1280), "white").save(buffer, "WEBP")
    image = buffer.getvalue()
    encoded = base64.b64encode(image).decode()
    counts = Counter()
    generated = []
    language_requests = []
    ledger = ProviderCostLedger(
        2,
        prior="0.080775",
        reserve=round(calculate_checklist_worst_case(), 9),
        path=tmp_path / "costs.json",
    )

    spec = importlib.util.spec_from_file_location(
        "comic_runner_cost_simulation", FIXTURE.parents[2] / "scripts/e2e/comic_e2e.py"
    )
    runner = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(runner)
    guard = runner.CostGuard(2, True, ledger_path=tmp_path / "costs.json", prior_spend=0.080775)

    def respond(request):
        guard.refresh()
        payload = json.loads(request.content)
        if request.url.path.endswith("/responses/input_tokens"):
            # Model-specific preflight count is replayed by the fake transport too.
            return httpx.Response(200, json={"input_tokens": 3271})
        if request.url.path.endswith("/responses"):
            counts["image"] += 1
            return httpx.Response(
                200, json={"output": [{"type": "image_generation_call", "result": encoded}]}
            )
        messages = payload["messages"]
        content = messages[-1]["content"]
        operation = "vision" if isinstance(content, list) else "moderation"
        if isinstance(content, str):
            try:
                operation = json.loads(content)["operation"]
            except (ValueError, KeyError):
                pass
        counts[operation] += 1
        if operation != "vision":
            language_requests.append((messages, payload["max_tokens"]))
        if operation == "vision":
            answer = '{"theme":"Sea","palette":[{"region_name":"sea","hex_color":"#123456","label":"Sea"}]}'
        else:
            answer = FakeLanguageAdapter().complete(
                "", [Message(role=m["role"], content=m["content"]) for m in messages]
            )
            if operation == "characters":
                # Four retained profiles exercise the estimate's ~0.6 USD variant.
                profile = json.loads(answer)[0]
                answer = json.dumps(
                    [{**profile, "name": name} for name in ("Nia", "Sol", "Ada", "Bo")]
                )
        inputs, outputs = USAGE[operation]
        return httpx.Response(
            200,
            json={
                "choices": [{"message": {"content": answer}, "finish_reason": "stop"}],
                "usage": {"prompt_tokens": inputs, "completion_tokens": outputs},
            },
        )

    kwargs = {
        "api_key": "mock-key",
        "base_url": "http://mock.meta.internal/v1",
        "transport": httpx.MockTransport(respond),
        "cost_ledger": ledger,
    }
    language = MetaLanguageAdapter(**kwargs, cost_stage="language")
    images = MetaImageAdapter(**kwargs, cost_stage="image")
    vision = MetaVisionAdapter(**kwargs, cost_stage="vision")
    moderation = ModerationService(language)
    script = FIXTURE.read_text()
    moderation.moderate_prompt(script)
    profiles, chapters, words, _ = ScriptService(language).analyze_script(script)
    assert panel_count(words) == 51 and chapters == 4 and len(profiles) == 4
    storage = FakeStorageAdapter()
    character_service = CharacterService(images, storage)
    references = []
    for profile in profiles:
        moderation.moderate_prompt(profile.description)
        reference, _, _ = character_service.generate_reference_image(
            profile.name, profile.name, profile.description, "hash", "comic"
        )
        references.append(reference)
    moderation.moderate_prompt(script)
    panels = PanelService(language)
    outlines = panels.generate_chapter_outline(script, chapters, profiles)
    for index, source in enumerate(split_text(script, chapters)):
        # The Go flow moderates the outline plus source in two chunks per chapter.
        moderation.moderate_prompt(source)
        count = (index + 1) * 51 // chapters - index * 51 // chapters
        text = panels.generate_panel_text(
            index + 1,
            outlines[index].title,
            outlines[index].summary,
            [p.name for p in profiles],
            count,
            source,
        )
        generated.extend(text)
        for page_index in range(3):
            page_panels = text[page_index * len(text) // 3 : (page_index + 1) * len(text) // 3]
            page = " ".join(p.scene_description for p in page_panels)
            for _ in range(1 + regenerations):
                moderation.moderate_prompt(page)
                draft, _, _ = DraftService(images, storage).generate_draft_sketch(
                    f"{index}-{page_index}",
                    page_index + 1,
                    page_panels,
                    "comic",
                    "hash",
                    references,
                )
                ink, _, _ = LineArtService(images, storage).generate_line_art(
                    f"{index}-{page_index}",
                    page_index + 1,
                    draft,
                    page_panels,
                    "comic",
                    "hash",
                    references,
                )
            ColourService(vision).suggest_palette(storage.get(ink), page)
    assert len(generated) == 51
    assert counts == {
        "characters": 7,
        "outline": 8,
        "panels": 12,
        "moderation": 38 + regenerations * 12,
        "image": 28 + regenerations * 24,
        "vision": 12,
    }
    # Explicit caller replays of bounded language requests model operator retries;
    # capped HTTP clients themselves continue to make zero automatic retries.
    bounded_replays = tuple(language_requests)
    for _ in range(replays):
        for messages, wire_cap in bounded_replays:
            language.complete(
                "",
                [Message(role=m["role"], content=m["content"]) for m in messages],
                max_tokens=wire_cap - language._reasoning_reserve,
            )
    guard.refresh()
    report = ledger.report()
    assert guard.alert_raised is bool(regenerations)
    assert capsys.readouterr().out.count("COST ALERT:") == bool(regenerations)
    assert report["unpriced_calls"] == 0
    assert report["cumulative_accounted_spend_usd"] + report["third_check_reserve_usd"] < 2
    assert report["actual_spend_usd"] == pytest.approx(
        0.594634 if not regenerations else 1.453432, abs=1e-9
    )
    assert counts["moderation"] == (38 + regenerations * 12) * (1 + replays)
    for call in report["calls"]:
        assert call["reserved_cost_usd"] >= call["actual_cost_usd"]
