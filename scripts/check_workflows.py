#!/usr/bin/env python3
"""Lint the GitHub workflows without network access (needs PyYAML).

Catches the mistakes that only show up as a red or silently skipped CI run: a job that
needs a job that does not exist, a required job missing from the `ci-success` gate, a
floating action reference, no explicit permissions, and `make` targets CI calls that the
Makefile does not define.
"""

from __future__ import annotations

import re
import sys
from pathlib import Path

import yaml

REPO_ROOT = Path(__file__).resolve().parent.parent
GATE_JOB = "ci-success"


def lint_workflow(name: str, workflow: dict, make_targets: set[str]) -> list[str]:
    problems = []
    # YAML 1.1 reads the bare key `on` as the boolean True.
    if "on" not in workflow and True not in workflow:
        problems.append(f"{name}: no trigger (`on`)")
    if "permissions" not in workflow:
        problems.append(f"{name}: no top-level `permissions`; default to read-only")
    jobs = workflow.get("jobs") or {}
    if not jobs:
        problems.append(f"{name}: no jobs")
    for job_id, job in jobs.items():
        if "runs-on" not in job and "uses" not in job:
            problems.append(f"{name}: job {job_id} has no `runs-on`")
        needs = job.get("needs", [])
        for needed in [needs] if isinstance(needs, str) else needs:
            if needed not in jobs:
                problems.append(f"{name}: job {job_id} needs unknown job {needed}")
        for step in job.get("steps", []):
            uses = step.get("uses")
            if uses and not uses.startswith("./") and not re.search(r"@(v?\d[\w.]*|[0-9a-f]{40})$", uses):
                problems.append(f"{name}: job {job_id} uses a floating action reference: {uses}")
            for target in re.findall(r"\bmake\s+([a-z][\w-]*)", step.get("run", "") or ""):
                if target not in make_targets:
                    problems.append(f"{name}: job {job_id} runs `make {target}`, which the Makefile lacks")
    return problems


def lint_gate(name: str, workflow: dict) -> list[str]:
    """Every other job must gate `ci-success`, or a red job could still merge."""
    jobs = workflow.get("jobs") or {}
    if GATE_JOB not in jobs:
        return []
    needs = jobs[GATE_JOB].get("needs", [])
    missing = sorted(set(jobs) - {GATE_JOB} - set(needs))
    return [f"{name}: job {job} is not in the {GATE_JOB} gate" for job in missing]


def make_targets_of(makefile: str) -> set[str]:
    return set(re.findall(r"^([a-z][\w-]*):", makefile, re.M))


def main() -> int:
    targets = make_targets_of((REPO_ROOT / "Makefile").read_text())
    problems = []
    files = sorted((REPO_ROOT / ".github" / "workflows").glob("*.y*ml"))
    if not files:
        problems.append("no workflows found")
    for path in files:
        workflow = yaml.safe_load(path.read_text())
        problems += lint_workflow(path.name, workflow, targets)
        problems += lint_gate(path.name, workflow)
    if problems:
        print("Workflow lint failed:", *problems, sep="\n  - ", file=sys.stderr)
        return 1
    print(f"{len(files)} workflows pass the lint.")
    return 0


if __name__ == "__main__":
    sys.exit(main())
