"""Unit tests for the hermetic guard script."""

import tempfile
import unittest
from pathlib import Path

from check_hermetic import check_file


class TestHermeticGuard(unittest.TestCase):
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
        with tempfile.NamedTemporaryFile("w+", suffix=".py") as f:
            f.write('API_URL = "https://graph.facebook.com/v19.0/me"\n')
            f.flush()
            violations = check_file(Path(f.name))
            self.assertTrue(any(v.rule == "NO_REAL_META_HOSTS" for v in violations))

    def test_meta_api_key_detected(self):
        cases = [
            'api_key = os.environ.get("META_API_KEY")',
            'apiKey := os.Getenv("META_API_KEY")',
            'const key = process.env.META_API_KEY;',
        ]
        for idx, case in enumerate(cases):
            with tempfile.NamedTemporaryFile("w+", suffix=f"case_{idx}.py") as f:
                f.write(f"{case}\n")
                f.flush()
                violations = check_file(Path(f.name))
                self.assertTrue(
                    any(v.rule == "NO_META_API_KEY_IN_CODE_OR_TESTS" for v in violations),
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
            f.write('db, err := sql.Open("postgres", "postgres://localhost:5432/test")\n')
            f.flush()
            violations = check_file(Path(f.name))
            self.assertTrue(any(v.rule == "NO_LIVE_DB_CONNECTIONS_IN_TESTS" for v in violations))


if __name__ == "__main__":
    unittest.main()
