"""Hermetic tests for the generated contract drift checker."""

from __future__ import annotations

import tempfile
import unittest
from pathlib import Path

from check_generated import compare_generated


class TestGeneratedContractDrift(unittest.TestCase):
    def test_modified_generated_file_is_reported_as_drift(self):
        with tempfile.TemporaryDirectory() as temp_dir:
            root = Path(temp_dir)
            generated_dir = root / "generated"
            generated_dir.mkdir()
            expected_files = {
                "types.go": root / "types.go",
                "models.py": root / "models.py",
            }

            for name, expected in expected_files.items():
                expected.write_text(f"checked-in {name}\n", encoding="utf-8")
                (generated_dir / name).write_text(f"checked-in {name}\n", encoding="utf-8")

            self.assertEqual(compare_generated(generated_dir, expected_files), [])

            (generated_dir / "models.py").write_text("modified generated model\n", encoding="utf-8")

            self.assertEqual(compare_generated(generated_dir, expected_files), ["models.py"])


if __name__ == "__main__":
    unittest.main()
