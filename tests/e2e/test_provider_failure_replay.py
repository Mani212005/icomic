"""Long comic failures through real Go HTTP, Python workers and replayed provider bodies."""

import importlib.util
import json
import os
import socket
import subprocess
import sys
import threading
from pathlib import Path

import httpx
import pytest
import uvicorn

from conftest import terminate_process, wait_for_health

sys.path.insert(0, str(Path(__file__).resolve().parents[2] / "ai"))
from app.adapters.factory import build_adapters_from_env, get_media_fetch_adapter
from app.adapters.image.fake import FakeImageAdapter
from app.adapters.image.meta import MetaImageAdapter
from app.adapters.language.fake import FakeLanguageAdapter
from app.adapters.language.meta import MetaLanguageAdapter
from app.adapters.language.port import Message
from app.adapters.vision.fake import FakeVisionAdapter
from app.adapters.vision.meta import MetaVisionAdapter
from app.main import create_app
from app.costs import GENERATION_POLICIES
from app.provider_costs import ProviderCostLedger, provider_operation
from app.provider_diagnostics import ProviderDiagnostics


@pytest.mark.parametrize(
    "target,shape,occurrence",
    [
        ("moderate_prompt", "http_404", 1),
        ("moderate_prompt", "moderation_length_873", 1),
        ("analyze_script", "schema_echo", 6),
        ("generate_character_reference", "invalid_image", 1),
        ("generate_chapter_outline", "schema_echo", 2),
        ("generate_panel_text", "truncated_json", 2),
        ("generate_draft_sketch", "http_400", 1),
        ("generate_line_art", "invalid_image", 1),
        ("suggest_colour_palette", "palette_list", 1),
        ("assemble_pdf", "corrupt_stored_image", 1),
        ("complete_with_reasoning_load", None, 0),
        ("complete_with_extras", None, 0),
    ],
)
def test_long_flow_stops_at_each_failed_stage(
    compiled_go_server, repo_root, tmp_path, monkeypatch, target, shape, occurrence
):
    fixtures = repo_root / "ai/tests/fixtures/meta/observed"

    def load(name):
        return json.loads((fixtures / f"{name}.json").read_text())

    calls = []
    operation_counts = {}
    ledger = ProviderCostLedger(2, path=tmp_path / "costs.json")
    diagnostics = ProviderDiagnostics(tmp_path / "diagnostics")

    def respond(request):
        if request.url.path.endswith("/responses/input_tokens"):
            return httpx.Response(200, json={"input_tokens": 10000})
        operation, _ = provider_operation.get()
        calls.append(operation)
        operation_counts[operation] = operation_counts.get(operation, 0) + 1
        if operation == target and operation_counts[operation] == occurrence:
            record = load(shape)
            return httpx.Response(record["status"], json=record["body"])
        payload = json.loads(request.content)
        if request.url.path.endswith("/responses"):
            return httpx.Response(200, json=load("webp_png_label")["body"])
        messages = payload["messages"]
        if isinstance(messages[-1]["content"], list):
            return httpx.Response(200, json=load("vision")["body"])
        text = FakeLanguageAdapter().complete("fake", [Message(**m) for m in messages])
        if target == "complete_with_extras" and operation == "analyze_script":
            template = json.loads(text)[0]
            text = json.dumps([{**template, "name": f"Hero{n:02}"} for n in range(10)])
        if target == "complete_with_reasoning_load" and operation == "moderate_prompt":
            policy = GENERATION_POLICIES["moderation"]
            assert payload["reasoning_effort"] == "minimal"
            assert payload["max_tokens"] == policy.answer_tokens + policy.reasoning_reserve
            observed = load("moderation_length_873")["body"]
            assert payload["max_tokens"] > observed["usage"]["completion_tokens"]
            observed["choices"][0]["message"]["content"] = "SAFE"
            observed["choices"][0]["finish_reason"] = "stop"
            return httpx.Response(200, json=observed)
        return httpx.Response(
            200,
            json={
                "choices": [{"message": {"content": text}, "finish_reason": "stop"}],
                "usage": {"prompt_tokens": 100, "completion_tokens": 50},
            },
        )

    kwargs = dict(
        api_key="hermetic-replay",
        base_url="http://mock.meta.internal/v1",
        transport=httpx.MockTransport(respond),
        cost_ledger=ledger,
        diagnostics=diagnostics,
    )
    adapters = build_adapters_from_env()
    adapters.language = FakeLanguageAdapter(replay_adapter=MetaLanguageAdapter(**kwargs))
    adapters.image = FakeImageAdapter(replay_adapter=MetaImageAdapter(**kwargs, cost_stage="image"))
    adapters.vision = FakeVisionAdapter(
        replay_adapter=MetaVisionAdapter(**kwargs, cost_stage="vision")
    )
    adapters.cost_ledger = ledger
    adapters.diagnostics = diagnostics
    original_get = adapters.storage.get
    export_reads = []

    def get_image(key):
        if target == "assemble_pdf" and provider_operation.get()[0] == target:
            export_reads.append(key)
            return b"\x89PNG\r\n\x1a\ninvalid-image"
        return original_get(key)

    monkeypatch.setattr(adapters.storage, "get", get_image)
    sock = socket.socket()
    sock.bind(("127.0.0.1", 0))
    ai_port = sock.getsockname()[1]
    ai_url = f"http://127.0.0.1:{ai_port}"
    adapters.storage.base_url = ai_url + "/storage/fake"
    adapters.media_fetch = get_media_fetch_adapter(adapters.storage)
    server = uvicorn.Server(uvicorn.Config(create_app(adapters), log_level="critical"))
    thread = threading.Thread(target=lambda: server.run(sockets=[sock]), daemon=True)
    thread.start()
    backend = None
    try:
        wait_for_health(ai_url + "/healthz")
        probe = socket.socket()
        probe.bind(("127.0.0.1", 0))
        go_port = probe.getsockname()[1]
        probe.close()
        env = {
            "PATH": os.environ["PATH"],
            "PORT": str(go_port),
            "PYTHON_AI_URL": ai_url,
            "DEV_AUTH_ENABLED": "true",
            "MEDIA_DIR": str(tmp_path / "media"),
            "ICOMIC_E2E_ISOLATED_STACK": "1",
            "ICOMIC_E2E_DAILY_IMAGES": "100",
            "ICOMIC_E2E_MONTHLY_IMAGES": "1000",
        }
        with (tmp_path / "go.log").open("w") as log:
            backend = subprocess.Popen(
                [str(compiled_go_server)], env=env, stdout=log, stderr=log, start_new_session=True
            )
        api_url = f"http://127.0.0.1:{go_port}"
        wait_for_health(api_url + "/healthz")
        spec = importlib.util.spec_from_file_location(
            "failure_runner", repo_root / "scripts/e2e/comic_e2e.py"
        )
        runner = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(runner)
        monkeypatch.setattr(runner, "API_URL", api_url)
        monkeypatch.setattr(runner, "STORAGE_URL", ai_url)
        monkeypatch.setattr(
            runner, "provision_run_user", lambda: "00000000-0000-0000-0000-000000000001"
        )
        guard = runner.CostGuard(float("inf"), False)
        guard.script_file = repo_root / "tests/fixtures/the_tide_clock.txt"
        if target.startswith("complete_with_"):
            pages, _ = runner.run_flow(guard, tmp_path)
            assert (guard.panel_count, pages, guard.pdf_page_count) == (51, 12, 13)
            if target == "complete_with_extras":
                characters = runner.json_request(f"/api/v1/projects/{guard.project_id}/characters")[
                    "characters"
                ]
                assert len(characters) == 10
                assert all(c.get("approved_at") for c in characters)
                extras = [
                    c for c in characters if c["visual_attributes"]["reference_role"] == "extra"
                ]
                assert len(extras) == 4 and all(not c.get("reference_image_url") for c in extras)
                assert operation_counts["generate_character_reference"] == 6
            else:
                moderation = [
                    c for c in ledger.report()["calls"] if c["operation"] == "moderate_prompt"
                ]
                assert moderation and all(c["usage"]["reasoning_tokens"] == 873 for c in moderation)
            return
        with pytest.raises(runner.E2EError):
            runner.run_flow(guard, tmp_path)
        assert not (tmp_path / "latest.pdf").exists()
        assert not any(key.startswith("exports/") for key in adapters.storage._blobs)
        if target != "assemble_pdf":
            assert calls[-1] == target
            assert operation_counts[target] == occurrence
            assert len(list((tmp_path / "diagnostics").glob("*.json"))) == 1
            failed_call = ledger.report()["calls"][-1]
            status = (
                runner.json_request(f"/api/v1/generation-jobs/{failed_call['job_id']}")
                if target != "moderate_prompt"
                else None
            )
            if status:
                expected = sum(
                    c["actual_cost_usd"] or 0
                    for c in ledger.report()["calls"]
                    if c["job_id"] == failed_call["job_id"]
                )
                assert status["job"]["status"] == "failed"
                assert status["job"]["actual_cost"] == pytest.approx(expected)
        else:
            assert export_reads
            assert not list((tmp_path / "diagnostics").glob("*.json"))
        project = guard.project_id
        characters = runner.json_request(f"/api/v1/projects/{project}/characters").get(
            "characters", []
        )
        if target in {"moderate_prompt", "analyze_script"}:
            assert not characters
        if target == "generate_character_reference":
            assert all(not c.get("reference_image_url") for c in characters)
        chapters = runner.json_request(f"/api/v1/projects/{project}/chapters").get("chapters") or []
        if target in {
            "moderate_prompt",
            "analyze_script",
            "generate_character_reference",
            "generate_chapter_outline",
        }:
            assert not chapters
        for chapter in chapters:
            panels = runner.json_request(f"/api/v1/chapters/{chapter['id']}/panels").get(
                "panels", []
            )
            pages = runner.json_request(f"/api/v1/chapters/{chapter['id']}/pages").get("pages", [])
            if target == "generate_panel_text":
                assert not panels and not pages
            if pages:
                if target == "generate_draft_sketch":
                    assert not pages[0].get("draft_image_url")
                elif target == "generate_line_art":
                    assert not pages[0].get("line_art_image_url")
                elif target == "suggest_colour_palette":
                    assert not pages[0].get("colour_approved_at")
    finally:
        if backend is not None:
            terminate_process(backend)
        server.should_exit = True
        thread.join(timeout=10)
        sock.close()
        assert not thread.is_alive()
