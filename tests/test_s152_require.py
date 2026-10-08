"""S152 — Requirements Engine conformance tests.

Guardrails for prerequisite evaluation: architecture with platform
gates and declared floors, CPU and memory with both sides compared,
GPU presence, per-class storage floors with missing data failing,
host port conflicts, ordered checks passing only jointly, invalid
manifests failing instead of grading, and optional floors keeping
older manifests valid. Behavior is covered by Go tests in
internal/catalog with fixture facts.
"""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def read(rel: str) -> str:
    return (REPO / rel).read_text(encoding="utf-8")


class TestRequireContract(unittest.TestCase):
    def test_engine_shapes(self):
        src = read("internal/catalog/requirements.go")
        for symbol in ("type HostFacts", "type RequirementReport",
                       "func Evaluate(", "architecture", '"memory"',
                       '"gpu"', "already bound"):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)


if __name__ == "__main__":
    unittest.main()
