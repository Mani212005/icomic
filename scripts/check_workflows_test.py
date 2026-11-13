"""Hermetic tests for the workflow linter."""

from __future__ import annotations

import unittest

from check_workflows import lint_gate, lint_workflow, make_targets_of

TARGETS = {"check", "test-e2e"}
GOOD = {
    True: {"push": None},
    "permissions": {"contents": "read"},
    "jobs": {
        "build": {"runs-on": "ubuntu-latest", "steps": [{"uses": "actions/checkout@v4"}]},
        "ci-success": {"runs-on": "ubuntu-latest", "needs": ["build"], "steps": []},
    },
}


def with_job(**job) -> dict:
    workflow = {**GOOD, "jobs": {**GOOD["jobs"]}}
    workflow["jobs"]["build"] = {"runs-on": "ubuntu-latest", **job}
    return workflow


class TestWorkflowLint(unittest.TestCase):
    def test_good_workflow_passes(self):
        self.assertEqual(lint_workflow("ci.yml", GOOD, TARGETS), [])
        self.assertEqual(lint_gate("ci.yml", GOOD), [])

    def test_unknown_need_is_reported(self):
        problems = lint_workflow("ci.yml", with_job(needs=["nope"]), TARGETS)
        self.assertEqual(problems, ["ci.yml: job build needs unknown job nope"])

    def test_floating_action_reference_is_reported(self):
        for floating in ("actions/checkout@main", "actions/checkout"):
            problems = lint_workflow("ci.yml", with_job(steps=[{"uses": floating}]), TARGETS)
            self.assertEqual(len(problems), 1, floating)
        pinned = with_job(steps=[{"uses": "actions/checkout@" + "a" * 40}])
        self.assertEqual(lint_workflow("ci.yml", pinned, TARGETS), [])

    def test_missing_make_target_is_reported(self):
        problems = lint_workflow("ci.yml", with_job(steps=[{"run": "make test-e2e\nmake gone"}]), TARGETS)
        self.assertEqual(problems, ["ci.yml: job build runs `make gone`, which the Makefile lacks"])

    def test_missing_permissions_trigger_and_runner_are_reported(self):
        workflow = {"jobs": {"build": {"steps": []}}}
        self.assertEqual(len(lint_workflow("ci.yml", workflow, TARGETS)), 3)

    def test_a_job_outside_the_gate_is_reported(self):
        workflow = {**GOOD, "jobs": {**GOOD["jobs"], "extra": {"runs-on": "ubuntu-latest"}}}
        self.assertEqual(lint_gate("ci.yml", workflow), ["ci.yml: job extra is not in the ci-success gate"])

    def test_make_targets_are_read_from_the_makefile(self):
        self.assertEqual(make_targets_of("all: check\n\ncheck: lint\n\t@true\n.PHONY: x\n"), {"all", "check"})


if __name__ == "__main__":
    unittest.main()
