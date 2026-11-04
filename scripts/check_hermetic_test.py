"""Unit tests for the hermetic guard script."""

import sys
import tempfile
import unittest
from unittest.mock import patch
from pathlib import Path
from types import SimpleNamespace

import check_hermetic
from check_hermetic import check_file

REPO_ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(REPO_ROOT / "ai"))
sys.path.insert(0, str(Path(__file__).resolve().parent / "e2e"))
from app.costs import actual_stage_cost
from comic_e2e import CostGuard, live_authorization_error, parse_args


class TestHermeticGuard(unittest.TestCase):
    def test_comic_artifact_directory_is_configurable(self):
        args = parse_args(["--artifact-dir", "/tmp/icomic-fake-proof"])
        self.assertEqual(args.artifact_dir, Path("/tmp/icomic-fake-proof"))
        self.assertFalse(args.live)

    def test_comic_live_guard_requires_flag_environment_and_cap(self):
        self.assertIsNotNone(live_authorization_error(False, {}, None))
        self.assertIsNotNone(live_authorization_error(True, {}, 2.0))
        self.assertIsNotNone(
            live_authorization_error(True, {"ICOMIC_E2E_LIVE": "1"}, 1.99)
        )
        self.assertIsNone(live_authorization_error(True, {"ICOMIC_E2E_LIVE": "1"}, 2.0))
        self.assertIsNotNone(
            live_authorization_error(True, {"CI": "true", "ICOMIC_E2E_LIVE": "1"}, 2.0)
        )

    def test_comic_cost_guard_refuses_call_above_remaining_cap(self):
        guard = CostGuard(cap=0.02, live=True)
        with self.assertRaisesRegex(RuntimeError, "cost guard refused"):
            guard.before_call("image")

    def test_actual_cost_uses_observed_tokens_and_meta_image_price(self):
        usage = SimpleNamespace(input_tokens=10, output_tokens=20)
        self.assertAlmostEqual(
            actual_stage_cost("language", SimpleNamespace(last_usage=usage)), 0.0000975
        )
        self.assertEqual(
            actual_stage_cost("image", SimpleNamespace(), "muse-image-1.0-fake"), 0.0
        )
        self.assertEqual(
            actual_stage_cost("image", SimpleNamespace(), "muse-image-1.0"), 0.01
        )

    def test_clean_file_passes(self):
        with tempfile.NamedTemporaryFile("w+", suffix="_test.py") as f:
            f.write("def test_health():\n    assert 1 == 1\n")
            f.flush()
            violations = check_file(Path(f.name))
            self.assertEqual(len(violations), 0)

    def test_live_pytest_marker_detected(self):
        with tempfile.NamedTemporaryFile("w+", suffix="_test.py") as f:
            f.write("@pytest.mark.live\ndef test_external_api():\n    pass\n")
            f.flush()
            violations = check_file(Path(f.name))
            self.assertTrue(any(v.rule == "NO_LIVE_PYTEST_MARKERS" for v in violations))

    def test_real_meta_host_detected(self):
        hosts = [
            "https://graph.facebook.com/v19.0/me",
            "https://api.meta.ai/v1/chat/completions",
            "https://api.openai.com/v1/chat/completions",
        ]
        for host in hosts:
            with tempfile.NamedTemporaryFile("w+", suffix=".py") as f:
                f.write(f'API_URL = "{host}"\n')
                f.flush()
                violations = check_file(Path(f.name))
                self.assertTrue(
                    any(v.rule == "NO_REAL_META_HOSTS" for v in violations),
                    f"Failed to detect real host in {host}",
                )

    def test_meta_api_key_detected(self):
        cases = [
            'api_key = os.environ.get("META_API_KEY")',
            'apiKey := os.Getenv("META_API_KEY")',
            "const key = process.env.META_API_KEY;",
        ]
        for idx, case in enumerate(cases):
            with tempfile.NamedTemporaryFile("w+", suffix=f"case_{idx}.py") as f:
                f.write(f"{case}\n")
                f.flush()
                violations = check_file(Path(f.name))
                self.assertTrue(
                    any(
                        v.rule == "NO_META_API_KEY_IN_CODE_OR_TESTS" for v in violations
                    ),
                    f"Failed to detect in {case}",
                )

    def test_workflow_db_service_detected(self):
        workflow_content = (
            "jobs:\n"
            "  test:\n"
            "    runs-on: ubuntu-latest\n"
            "    services:\n"
            "      postgres:\n"
            "        image: postgres:15\n"
        )
        with tempfile.TemporaryDirectory() as tmpdir:
            wf_dir = Path(tmpdir) / ".github" / "workflows"
            wf_dir.mkdir(parents=True)
            wf_file = wf_dir / "test.yml"
            wf_file.write_text(workflow_content)
            violations = check_file(wf_file)
            self.assertTrue(any(v.rule == "NO_DB_SERVICES_IN_CI" for v in violations))

    def test_test_db_connect_detected(self):
        with tempfile.NamedTemporaryFile("w+", suffix="_test.go") as f:
            f.write(
                'db, err := sql.Open("postgres", "postgres://localhost:5432/test")\n'
            )
            f.flush()
            violations = check_file(Path(f.name))
            self.assertTrue(
                any(v.rule == "NO_LIVE_DB_CONNECTIONS_IN_TESTS" for v in violations)
            )

    def test_only_tagged_realdb_test_may_use_loopback_postgres(self):
        with tempfile.TemporaryDirectory() as tmpdir:
            file_path = Path(tmpdir) / "backend" / "internal" / "db" / "postgres_realdb_test.go"
            file_path.parent.mkdir(parents=True)
            file_path.write_text(
                '//go:build realdb\n'
                'const local = "postgres://localhost:5432/test"\n'
                'url := os.Getenv("TEST_DATABASE_URL")\n'
            )
            # The exception is bound to the repository path, so the same file in an
            # arbitrary location stays subject to the default hermetic rule.
            violations = check_file(file_path)
            self.assertTrue(any(v.rule == "NO_LIVE_DB_CONNECTIONS_IN_TESTS" for v in violations))

            with patch.object(check_hermetic, "REALDB_TEST_FILE", str(file_path)):
                file_path.write_text(
                    '//go:build realdb\n'
                    'const local = "postgres://localhost:5432/test"\n'
                    'url := os.Getenv("TEST_DATABASE_URL")\n'
                )
                self.assertEqual(check_file(file_path), [])
                file_path.write_text(
                    '//go:build realdb\n'
                    'const external = "postgres://db.example.test:5432/test"\n'
                )
                self.assertTrue(
                    any(v.rule == "NO_LIVE_DB_CONNECTIONS_IN_TESTS" for v in check_file(file_path))
                )
                file_path.write_text(
                    '//go:build realdb\n'
                    'const redis = "redis://localhost:6379/0"\n'
                )
                self.assertTrue(
                    any(v.rule == "NO_LIVE_DB_CONNECTIONS_IN_TESTS" for v in check_file(file_path))
                )

    def test_designated_ci_realdb_job_is_the_only_workflow_exception(self):
        workflow = (
            "jobs:\n"
            "  backend-realdb:\n"
            "    services:\n"
            "      postgres:\n"
            "        image: postgres:16-alpine\n"
            "    steps:\n"
            "      - env:\n"
            "          TEST_DATABASE_URL: postgres://icomic@127.0.0.1:5432/test\n"
            "        run: make test-backend-realdb\n"
        )
        with tempfile.TemporaryDirectory() as tmpdir:
            wf_dir = Path(tmpdir) / ".github" / "workflows"
            wf_dir.mkdir(parents=True)
            wf_file = wf_dir / "ci.yml"
            wf_file.write_text(workflow)
            self.assertTrue(any(v.rule == "NO_DB_SERVICES_IN_CI" for v in check_file(wf_file)))

            with patch.object(check_hermetic, "REALDB_CI_WORKFLOW", str(wf_file)):
                wf_file.write_text(workflow)
                self.assertEqual(check_file(wf_file), [])
                wf_file.write_text(workflow.replace("make test-backend-realdb", "go test ./..."))
                self.assertTrue(any(v.rule == "NO_DB_SERVICES_IN_CI" for v in check_file(wf_file)))

    def test_allowed_meta_http_file_permits_host_and_key(self):
        with tempfile.TemporaryDirectory() as tmpdir:
            file_path = Path(tmpdir) / "ai" / "app" / "adapters" / "meta_http.py"
            file_path.parent.mkdir(parents=True)
            file_path.write_text(
                'DEFAULT_URL = "https://api.meta.ai/v1"\n'
                'api_key = os.getenv("META_API_KEY")\n'
            )
            violations = check_file(file_path)
            self.assertEqual(len(violations), 0)

    def test_allowed_factory_file_permits_lazy_key_but_forbids_host(self):
        with tempfile.TemporaryDirectory() as tmpdir:
            file_path = Path(tmpdir) / "ai" / "app" / "adapters" / "factory.py"
            file_path.parent.mkdir(parents=True)
            file_path.write_text('api_key = os.getenv("META_API_KEY")\n')
            violations = check_file(file_path)
            self.assertEqual(len(violations), 0)

            # Factory is NOT allowed to define live host URLs
            file_path.write_text('HOST = "https://api.meta.ai/v1"\n')
            violations = check_file(file_path)
            self.assertTrue(any(v.rule == "NO_REAL_META_HOSTS" for v in violations))

    def test_disallowed_files_fail_for_meta_host_and_key(self):
        disallowed_paths = [
            ("ai", "tests", "test_meta.py"),
            ("ai", "tests", "fixtures", "meta", "chat.json"),
            ("ai", "app", "adapters", "image", "meta.py"),
            (".github", "workflows", "ci.yml"),
        ]
        for parts in disallowed_paths:
            with tempfile.TemporaryDirectory() as tmpdir:
                file_path = Path(tmpdir).joinpath(*parts)
                file_path.parent.mkdir(parents=True, exist_ok=True)

                # Test live host rejection
                file_path.write_text('url = "https://api.meta.ai/v1"\n')
                violations = check_file(file_path)
                self.assertTrue(
                    any(v.rule == "NO_REAL_META_HOSTS" for v in violations),
                    f"Allowed live host in disallowed path: {parts}",
                )

                # Test META_API_KEY rejection
                file_path.write_text('key = os.getenv("META_API_KEY")\n')
                violations = check_file(file_path)
                self.assertTrue(
                    any(
                        v.rule == "NO_META_API_KEY_IN_CODE_OR_TESTS" for v in violations
                    ),
                    f"Allowed META_API_KEY in disallowed path: {parts}",
                )


if __name__ == "__main__":
    unittest.main()
