#!/usr/bin/env python3
"""Hermetic guard verification script.

Ensures that tests and workflows in the Icomic repository remain strictly hermetic:
1. No live API references (no live pytest markers, no real Meta hosts, no META_API_KEY read in code/tests).
2. No live database requirements (no Postgres or Redis services in workflows, no live DB connections in tests).

Narrow exception:
- The default Meta API host ("api.meta.ai") is permitted ONLY in ai/app/adapters/meta_http.py.
- The lazy environment variable read for META_API_KEY is permitted ONLY in:
  - ai/app/adapters/meta_http.py
  - ai/app/adapters/factory.py
Any other occurrence (in tests, workflows, fixtures, or other adapter modules) fails CI.
"""

from __future__ import annotations

import os
import re
import sys
from pathlib import Path

REPO_ROOT = Path(__file__).resolve().parent.parent

# Directories to inspect
INSPECT_DIRS = ["ai", "backend", "contracts", "frontend", ".github"]

# Directories and files to exclude from inspection
EXCLUDE_DIRS = {
    ".git",
    ".pytest_cache",
    ".ruff_cache",
    "node_modules",
    ".venv",
    "venv",
    "dist",
    "build",
    ".svelte-kit",
    "__pycache__",
    "docs",  # Documentation may mention API keys or architecture references
    "scripts",  # Guard scripts themselves and manual verification scripts
}

EXCLUDE_FILES = {
    "package-lock.json",
    "uv.lock",
}

# Permitted narrow exceptions
ALLOWED_META_HOST_FILES = {
    "ai/app/adapters/meta_http.py",
}

ALLOWED_META_API_KEY_FILES = {
    "ai/app/adapters/meta_http.py",
    "ai/app/adapters/factory.py",
}

# Real Meta & live external API host patterns
REAL_META_HOST_PATTERNS = [
    re.compile(r"api\.meta\.ai", re.IGNORECASE),
    re.compile(r"graph\.facebook\.com", re.IGNORECASE),
    re.compile(r"api\.meta\.com", re.IGNORECASE),
    re.compile(r"facebook\.com/api", re.IGNORECASE),
    re.compile(r"meta\.com/api", re.IGNORECASE),
    re.compile(r"llama-api\.com", re.IGNORECASE),
    re.compile(r"api\.together\.xyz", re.IGNORECASE),
    re.compile(r"api\.openai\.com", re.IGNORECASE),
    re.compile(r"api\.anthropic\.com", re.IGNORECASE),
]

# Live pytest marker patterns
LIVE_PYTEST_PATTERNS = [
    re.compile(r"@pytest\.mark\.(live|network|external|integration_live)"),
    re.compile(r"pytest\.mark\.(live|network|external|integration_live)"),
]

# Live pytest CLI marker selections (-m live, etc.)
LIVE_PYTEST_CLI_PATTERNS = [
    re.compile(r"pytest\s+.*-m\s+['\"]?live['\"]?"),
    re.compile(r"uv\s+run\s+pytest\s+.*-m\s+['\"]?live['\"]?"),
]

# Patterns reading META_API_KEY
META_API_KEY_PATTERNS = [
    re.compile(r"os\.(?:environ\.get|getenv|environ\[)\s*\(?\s*['\"]META_API_KEY['\"]"),
    re.compile(r"os\.Getenv\s*\(\s*['\"]META_API_KEY['\"]"),
    re.compile(r"os\.LookupEnv\s*\(\s*['\"]META_API_KEY['\"]"),
    re.compile(r"process\.env\.META_API_KEY"),
    re.compile(r"process\.env\[['\"]META_API_KEY['\"]\]"),
    re.compile(r"META_API_KEY"),
]

# Database service / runner requirement patterns in workflows
WORKFLOW_DB_SERVICE_PATTERNS = [
    re.compile(r"services:\s*.*\b(?:postgres|postgresql|redis)\b", re.IGNORECASE | re.DOTALL),
    re.compile(r"image:\s*['\"]?(?:postgres|redis)(?::[a-zA-Z0-9._-]+)?['\"]?", re.IGNORECASE),
    re.compile(r"(?:docker\s+run|service\s+start|systemctl\s+start)\s+.*(?:postgres|redis)", re.IGNORECASE),
]

# Live database connection requirements in test files
TEST_DB_CONNECT_PATTERNS = [
    re.compile(r"(?:postgres|postgresql|redis)://[a-zA-Z0-9_.-]+:[0-9]+", re.IGNORECASE),
    re.compile(r"localhost:(?:5432|6379)"),
    re.compile(r"127\.0\.0\.1:(?:5432|6379)"),
]


class Violation:
    def __init__(self, path: Path, line_number: int, rule: str, snippet: str):
        self.path = path
        self.line_number = line_number
        self.rule = rule
        self.snippet = snippet

    def __str__(self) -> str:
        rel_path = self.path.relative_to(REPO_ROOT) if self.path.is_relative_to(REPO_ROOT) else self.path
        return f"{rel_path}:{self.line_number} [{self.rule}] -> {self.snippet.strip()}"


def should_skip(path: Path) -> bool:
    if path.name in EXCLUDE_FILES:
        return True
    for part in path.parts:
        if part in EXCLUDE_DIRS:
            return True
    return False


def get_files_to_check(root: Path) -> list[Path]:
    files: list[Path] = []
    for dir_name in INSPECT_DIRS:
        target_dir = root / dir_name
        if not target_dir.exists():
            continue
        for current_root, dirs, filenames in os.walk(target_dir):
            current_path = Path(current_root)
            dirs[:] = [d for d in dirs if d not in EXCLUDE_DIRS]
            for filename in filenames:
                file_path = current_path / filename
                if not should_skip(file_path):
                    files.append(file_path)
    return files


def check_file(path: Path) -> list[Violation]:
    violations: list[Violation] = []
    is_test_file = (
        "test" in path.name.lower()
        or "tests" in path.parts
        or path.name.endswith("_test.go")
        or path.name.endswith(".test.js")
        or path.name.endswith(".test.ts")
    )
    is_workflow = ".github" in path.parts and path.suffix in [".yml", ".yaml"]
    is_config = path.name in ["pyproject.toml", "pytest.ini", "Makefile", "package.json"]

    try:
        rel_path_str = str(path.relative_to(REPO_ROOT)).replace("\\", "/")
    except ValueError:
        rel_path_str = str(path).replace("\\", "/")

    is_allowed_meta_host = (
        rel_path_str in ALLOWED_META_HOST_FILES
        or path.parts[-4:] == ("ai", "app", "adapters", "meta_http.py")
    )

    is_allowed_meta_api_key = (
        rel_path_str in ALLOWED_META_API_KEY_FILES
        or path.parts[-4:] in (
            ("ai", "app", "adapters", "meta_http.py"),
            ("ai", "app", "adapters", "factory.py"),
        )
    )

    try:
        with open(path, "r", encoding="utf-8", errors="replace") as f:
            lines = f.readlines()
    except Exception as e:
        print(f"Warning: Failed to read {path}: {e}", file=sys.stderr)
        return violations

    # 1. Live pytest markers in test files or configs
    if is_test_file or is_config or is_workflow:
        for line_idx, line in enumerate(lines, 1):
            if is_test_file:
                for pattern in LIVE_PYTEST_PATTERNS:
                    if pattern.search(line):
                        violations.append(
                            Violation(path, line_idx, "NO_LIVE_PYTEST_MARKERS", line)
                        )
            if is_workflow or is_config:
                for pattern in LIVE_PYTEST_CLI_PATTERNS:
                    if pattern.search(line):
                        violations.append(
                            Violation(path, line_idx, "NO_LIVE_PYTEST_CLI_MARKERS", line)
                        )

    # 2. Real Meta API host references (permitted only in meta_http.py)
    if not is_allowed_meta_host:
        for line_idx, line in enumerate(lines, 1):
            for pattern in REAL_META_HOST_PATTERNS:
                if pattern.search(line):
                    violations.append(
                        Violation(path, line_idx, "NO_REAL_META_HOSTS", line)
                    )

    # 3. META_API_KEY read in code, tests, or workflows (permitted only in meta_http.py and factory.py)
    if not is_allowed_meta_api_key:
        for line_idx, line in enumerate(lines, 1):
            for pattern in META_API_KEY_PATTERNS:
                if pattern.search(line):
                    violations.append(
                        Violation(path, line_idx, "NO_META_API_KEY_IN_CODE_OR_TESTS", line)
                    )

    # 4. Postgres or Redis requirement in workflows
    if is_workflow:
        for line_idx, line in enumerate(lines, 1):
            for pattern in WORKFLOW_DB_SERVICE_PATTERNS:
                if pattern.search(line):
                    violations.append(
                        Violation(path, line_idx, "NO_DB_SERVICES_IN_CI", line)
                    )

    # 5. Live DB connections required in tests
    if is_test_file:
        for line_idx, line in enumerate(lines, 1):
            for pattern in TEST_DB_CONNECT_PATTERNS:
                if pattern.search(line):
                    violations.append(
                        Violation(path, line_idx, "NO_LIVE_DB_CONNECTIONS_IN_TESTS", line)
                    )

    return violations


def run_checks(root: Path) -> int:
    print(f"Running hermetic guard checks on repository at {root}...")
    files = get_files_to_check(root)
    print(f"Scanning {len(files)} files across {', '.join(INSPECT_DIRS)}...")

    all_violations: list[Violation] = []
    for file_path in files:
        violations = check_file(file_path)
        all_violations.extend(violations)

    if all_violations:
        print(f"\n[FAILED] Hermetic guard found {len(all_violations)} violation(s):", file=sys.stderr)
        for v in all_violations:
            print(f"  - {v}", file=sys.stderr)
        return 1

    print("[PASSED] All hermetic guard checks passed successfully.")
    print("  - Zero live pytest markers or -m live invocations")
    print("  - Zero unauthorized real Meta or third-party AI provider hosts")
    print("  - Zero unauthorized META_API_KEY reads outside factory/meta_http")
    print("  - Zero Postgres/Redis dependencies in CI workflows or tests")
    return 0


if __name__ == "__main__":
    sys.exit(run_checks(REPO_ROOT))
