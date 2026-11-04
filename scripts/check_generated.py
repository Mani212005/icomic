#!/usr/bin/env python3
"""Compare regenerated OpenAPI models with the checked-in files."""

from __future__ import annotations

import argparse
import sys
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parent.parent
GENERATED_FILES = {
    "types.go": REPO_ROOT / "backend/gen/types.go",
    "models.py": REPO_ROOT / "ai/app/gen/models.py",
}


def compare_generated(generated_dir: Path, expected_files: dict[str, Path]) -> list[str]:
    """Return checked-in files whose contents differ from regenerated output."""
    return [
        generated_name
        for generated_name, expected_path in expected_files.items()
        if not (generated_dir / generated_name).is_file()
        or generated_dir.joinpath(generated_name).read_bytes() != expected_path.read_bytes()
    ]


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--generated-dir", type=Path, required=True)
    args = parser.parse_args()

    drifted_files = compare_generated(args.generated_dir, GENERATED_FILES)
    if drifted_files:
        print("OpenAPI generated contract models are out of date:", file=sys.stderr)
        for filename in drifted_files:
            print(f"  - {filename}", file=sys.stderr)
        print("Run 'make generate-contract-models' and commit the result.", file=sys.stderr)
        return 1

    print("OpenAPI generated contract models are up to date.")
    return 0


if __name__ == "__main__":
    sys.exit(main())
