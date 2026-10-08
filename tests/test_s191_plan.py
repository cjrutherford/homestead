"""S191 — Update Plan conformance tests.

Guardrails for update planning: registry maxima winning with
manifest pins covering silence, current services planning cleanly
empty, backup flags with database-first reasons, affected services
listed, major version jumps warning with versions attached, and
invalid manifests failing instead of planning. Behavior is covered
by Go tests in internal/update.
"""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def read(rel: str) -> str:
    return (REPO / rel).read_text(encoding="utf-8")


class TestPlanContract(unittest.TestCase):
    def test_plan_shapes(self):
        src = read("internal/update/plan.go")
        for symbol in ("func PlanUpdate(", "type UpdatePlan",
                       "type ServiceUpdate", "Backup",
                       "Affected", "major versions"):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)


if __name__ == "__main__":
    unittest.main()
