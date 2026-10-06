"""S054 — Atomic Firewall Apply conformance tests.

Guardrails for mutation: pre-check before commit, one atomic transaction
(add table, scoped flush, chain plus rules), post-verify against the live
table, temp scripts at 0600 and removed — and nothing else. Only the
homestead table ever appears; no ruleset-wide flush, no destroy, no
serving, no new dependencies. Behavior is covered by Go tests in
internal/firewall with stubbed execution.
"""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def read(rel: str) -> str:
    return (REPO / rel).read_text(encoding="utf-8")


class TestApplyContract(unittest.TestCase):
    def test_entry_points(self):
        src = read("internal/firewall/apply.go")
        self.assertIn("func ApplySet(", src)
        self.assertIn("func BuildApplyScript(", src)

    def test_check_apply_verify_order(self):
        src = read("internal/firewall/apply.go")
        check = src.index('"--check"')
        apply = src.index('"--file", path')
        self.assertLess(check, apply)
        self.assertIn("Build(set, live)", src)
        self.assertIn("InSync", src)

    def test_scoped_statements(self):
        src = read("internal/firewall/apply.go")
        self.assertIn('"add table %s %s\\n"', src)
        self.assertIn('"flush table %s %s\\n"', src)
        self.assertIn('"add chain %s %s input', src)
        self.assertIn('"add rule %s %s input %s\\n"', src)

    def test_temp_hygiene(self):
        src = read("internal/firewall/apply.go")
        self.assertIn("os.CreateTemp", src)
        self.assertIn("defer os.Remove(path)", src)

    def test_no_broad_mutation(self):
        """Only the homestead table: never ruleset flush/destroy, no serve."""
        src = read("internal/firewall/apply.go")
        for banned in ('"flush ruleset"', '"destroy"', "net/http",
                       "HandleFunc", "exec.Command"):
            with self.subTest(banned=banned):
                self.assertNotIn(banned, src)


if __name__ == "__main__":
    unittest.main()
