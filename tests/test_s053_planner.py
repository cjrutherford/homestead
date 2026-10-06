"""S053 — Firewall Planner conformance tests.

Guardrails for reviewed diffs: desired rules diffed against canonical
live rules into create/add/remove/update changes with the rendered script
attached; unknown live shapes surface as foreign removals rather than
errors; planning never applies, serves, writes files, or shells out
beyond the shared read-only runner. Behavior is covered by Go tests in
internal/firewall against nft JSON fixtures.
"""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def read(rel: str) -> str:
    return (REPO / rel).read_text(encoding="utf-8")


class TestPlannerContract(unittest.TestCase):
    def test_entry_points(self):
        src = read("internal/firewall/plan.go")
        for symbol in ("func Build(", "func ParseLiveTable(", "func LiveTable(",
                       "type Plan struct", "type Change struct", "type LiveRule struct",
                       "ErrNoTable"):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)

    def test_change_vocabulary(self):
        src = read("internal/firewall/plan.go")
        for action in ("create-table", '"add"', '"remove"', '"update"'):
            with self.subTest(action=action):
                self.assertIn(action, src)

    def test_foreign_rules_surface(self):
        src = read("internal/firewall/plan.go")
        self.assertIn("foreign-", src)
        self.assertIn("stripped", src)

    def test_planning_only(self):
        """Apply is S054: no -f apply, no file writes, no serving here."""
        src = read("internal/firewall/plan.go")
        for banned in ("net/http", "HandleFunc", "os.WriteFile", '"-f"',
                       '"flush"', '"destroy"', "exec.Command"):
            with self.subTest(banned=banned):
                self.assertNotIn(banned, src)


if __name__ == "__main__":
    unittest.main()
