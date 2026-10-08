"""S185 — Restore Planner conformance tests.

Guardrails for restore preview: runs inventoried newest-first with
foreign files and partial runs staying out, entries defaulting to
provenance mounts with missing provenance requiring explicit
targets, existing targets marking destructive with named warnings,
database entries warning quiescence, and empty runs refused. Plans
preview only; execution belongs next slice. Behavior is covered by
Go tests in internal/backup against seeded runs.
"""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def read(rel: str) -> str:
    return (REPO / rel).read_text(encoding="utf-8")


class TestRestorePlanContract(unittest.TestCase):
    def test_planner_shapes(self):
        src = read("internal/backup/restore.go")
        for symbol in ("func ListRuns(", "func PlanRestore(",
                       "type RestorePlan", "type PlannedEntry",
                       "overwrites", "explicit target"):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)


if __name__ == "__main__":
    unittest.main()
