"""S192 — Safe Application Update conformance tests.

Guardrails for applied updates: optional backup hooks with policy
left to callers, pulls before rewrites, pin rewrites requiring every
move to land, up with running-state verification, rollback
rewriting and re-serving on verify failure with both outcomes
reported, and empty or undescribed plans refused before anything
runs. Behavior is covered by Go tests in internal/update with
stubbed execution.
"""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def read(rel: str) -> str:
    return (REPO / rel).read_text(encoding="utf-8")


class TestApplyContract(unittest.TestCase):
    def test_apply_shapes(self):
        src = read("internal/update/apply.go")
        for symbol in ("func UpdateDefinition(",
                       '"app.update"', "rolled back"):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)


if __name__ == "__main__":
    unittest.main()
