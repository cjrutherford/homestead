"""S074 — Compose Stack Model conformance tests.

Guardrails for stack state: definitions with validated names and layout
directories, observed state grouped from Compose labels with one-offs
excluded, and wanted flags from the desired set. Lifecycle decisions
belong to S075; serving belongs to later slices still. Behavior is
covered by Go tests in internal/docker.
"""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def read(rel: str) -> str:
    return (REPO / rel).read_text(encoding="utf-8")


class TestStackContract(unittest.TestCase):
    def test_model_present(self):
        src = read("internal/docker/stack.go")
        for symbol in ("type Stack struct", "type ObservedStack struct",
                       "type ObservedService struct", "func DirFor(",
                       "func (s Stack) Validate(", "func ObserveStacks(",
                       "LabelProject", "LabelService", "LabelOneOff"):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)

    def test_desired_observed_split(self):
        src = read("internal/docker/stack.go")
        self.assertIn("Wanted", src)
        self.assertIn("Running", src)

    def test_no_lifecycle_or_serving(self):
        """Decisions are S075; endpoints arrive with their slices."""
        src = read("internal/docker/stack.go")
        for banned in ("net/http", "HandleFunc", "os/exec", "exec.Command",
                       "os.WriteFile", "up ", "down ", "restart"):
            with self.subTest(banned=banned):
                self.assertNotIn(banned, src)


if __name__ == "__main__":
    unittest.main()
