"""The complete long-script flow stays hermetic in the default E2E suite."""

import importlib.util
import os
import subprocess

from conftest import find_free_port, terminate_process, wait_for_health


def test_long_comic_has_all_panels_pages_and_pdf_pages(
    compiled_go_server, ai_service, tmp_path, repo_root, monkeypatch
):
    port = find_free_port()
    env = {
        "PATH": os.environ["PATH"],
        "PORT": str(port),
        "PYTHON_AI_URL": ai_service["base_url"],
        "DEV_AUTH_ENABLED": "true",
        "MEDIA_DIR": str(tmp_path / "media"),
    }
    with (tmp_path / "go.log").open("w") as log:
        proc = subprocess.Popen(
            [str(compiled_go_server)], env=env, stdout=log, stderr=log, start_new_session=True
        )
        try:
            wait_for_health(f"http://127.0.0.1:{port}/healthz")
            spec = importlib.util.spec_from_file_location(
                "multipage_runner", repo_root / "scripts/e2e/comic_e2e.py"
            )
            runner = importlib.util.module_from_spec(spec)
            spec.loader.exec_module(runner)
            monkeypatch.setattr(runner, "API_URL", f"http://127.0.0.1:{port}")
            monkeypatch.setattr(runner, "STORAGE_URL", ai_service["base_url"])
            # This fresh in-memory server has exactly one supported dev fixture.
            # The PostgreSQL runner provisions a distinct UUID and ledger per run.
            monkeypatch.setattr(
                runner, "provision_run_user", lambda: "00000000-0000-0000-0000-000000000001"
            )
            guard = runner.CostGuard(float("inf"), False)
            guard.script_file = repo_root / "tests/fixtures/the_tide_clock.txt"
            pages, _ = runner.run_flow(guard, tmp_path)
            assert guard.panel_count == 51
            assert pages == 12
            assert guard.pdf_page_count == 13
            assert runner.verify_pdf((tmp_path / "latest.pdf").read_bytes(), 12) == 13
        finally:
            terminate_process(proc)
