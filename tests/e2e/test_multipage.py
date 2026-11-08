"""The complete long-script flow stays hermetic in the default E2E suite."""

import importlib.util
import os
import subprocess
from urllib.parse import urlsplit

import httpx
import pytest
from conftest import find_free_port, terminate_process, wait_for_health


def media_key(url):
    """Stored artifact identity: the scoped link's query changes with every issue, its path does not."""
    return urlsplit(url).path if url else url


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
            [str(compiled_go_server)],
            env=env,
            stdout=log,
            stderr=log,
            start_new_session=True,
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
                runner,
                "provision_run_user",
                lambda: "00000000-0000-0000-0000-000000000001",
            )
            guard = runner.CostGuard(float("inf"), False)
            guard.script_file = repo_root / "tests/fixtures/the_tide_clock.txt"
            pages, _ = runner.run_flow(guard, tmp_path)
            assert guard.panel_count == 51
            assert pages == 12
            assert guard.pdf_page_count == 13
            assert runner.verify_pdf((tmp_path / "latest.pdf").read_bytes(), 12) == 13

            # Reuse the paid project and approvals. Only the four explicitly
            # selected finishes may generate new image work; drafts stay intact.
            project_id = guard.project_id
            chapters = runner.json_request(f"/api/v1/projects/{project_id}/chapters")["chapters"]

            def saved_pages():
                return [
                    page
                    for chapter in chapters
                    for page in runner.json_request(f"/api/v1/chapters/{chapter['id']}/pages")[
                        "pages"
                    ]
                ]

            original = saved_pages()
            selected = {1, 6, 8, 9}
            sources = {}

            def fetch_source(url):
                response = httpx.get(url)
                response.raise_for_status()
                return response.content

            for number in selected:
                page = original[number - 1]
                url = page.get("painted_page_image_url") or page["line_art_image_url"]
                sources[url] = fetch_source(url)
            before = runner.json_request("/api/v1/me/credit-usage")["balance"]
            resumed = runner.CostGuard(float("inf"), False)
            resumed.script_file = guard.script_file
            pages, same_project = runner.run_flow(
                resumed,
                tmp_path / "resumed",
                resume_project_id=project_id,
                resume_user_id=guard.user_id,
                repaint_pages=selected,
            )
            assert same_project == project_id and (
                resumed.panel_count,
                pages,
                resumed.pdf_page_count,
            ) == (51, 12, 13)
            after = runner.json_request("/api/v1/me/credit-usage")["balance"]
            assert before - after == pytest.approx(0.04)
            updated = saved_pages()
            for number, (old, new) in enumerate(zip(original, updated, strict=True), 1):
                assert media_key(old["draft_image_url"]) == media_key(new["draft_image_url"])
                assert old["draft_image_job_id"] == new["draft_image_job_id"]
                if number in selected:
                    assert new["painted_page_image_job_id"] != old.get("painted_page_image_job_id")
                    assert new["painted_page_approved_at"]
                else:
                    assert media_key(old.get("painted_page_image_url")) == media_key(
                        new.get("painted_page_image_url")
                    )
            assert media_key(original[0]["line_art_image_url"]) == media_key(
                updated[0]["line_art_image_url"]
            )
            for url, content in sources.items():
                assert fetch_source(url) == content

            # A later selective repaint must keep page1's newer painted finish,
            # rather than reapprove its retained historical ink/colour variant.
            requests = []
            original_request = runner.json_request

            def record_request(path, method="GET", *args, **kwargs):
                if method != "GET":
                    requests.append(path)
                return original_request(path, method, *args, **kwargs)

            monkeypatch.setattr(runner, "json_request", record_request)
            later = runner.CostGuard(float("inf"), False)
            later.script_file = guard.script_file
            runner.run_flow(
                later,
                tmp_path / "later",
                resume_project_id=project_id,
                resume_user_id=guard.user_id,
                repaint_pages={4, 5, 10},
            )
            last_balance = runner.json_request("/api/v1/me/credit-usage")["balance"]
            assert after - last_balance == pytest.approx(0.03)
            assert later.pdf_page_count == 13
            assert not any("variant=ink" in path or "/colour/" in path for path in requests)
            assert media_key(saved_pages()[0]["painted_page_image_url"]) == media_key(
                updated[0]["painted_page_image_url"]
            )
        finally:
            terminate_process(proc)
