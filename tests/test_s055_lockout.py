"""S055 — Lockout Protection conformance tests.

Guardrails against self-lockout: the guard proves an accept path (or
accept-default silence) for the administrator's connection before any
mutation, with first-match shadowing semantics; snapshots capture exact
table text for rollback, restoring byte-equality or failing loudly.
Guarding composes with S054 apply; nothing here serves, writes files
outside temp scripts, or reaches beyond the homestead table.
"""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def read(rel: str) -> str:
    return (REPO / rel).read_text(encoding="utf-8")


class TestGuardContract(unittest.TestCase):
    def test_entry_points(self):
        src = read("internal/firewall/guard.go")
        for symbol in ("type Connection struct", "func (c Connection) Validate(",
                       "func (r Rule) Matches(", "func Guard(", "func ApplyGuarded("):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)

    def test_first_match_semantics(self):
        src = read("internal/firewall/guard.go")
        self.assertIn("blocked by rule", src)
        self.assertIn("defaults to accept", src)


class TestRollbackContract(unittest.TestCase):
    def test_entry_points(self):
        src = read("internal/firewall/rollback.go")
        for symbol in ("type Snapshot struct", "func TakeSnapshot(",
                       "func (s Snapshot) Restore("):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)

    def test_scoped_restore(self):
        src = read("internal/firewall/rollback.go")
        self.assertIn('"delete table %s %s\\n%s"', src)
        self.assertIn("rollback mismatch", src)

    def test_no_broad_mutation(self):
        """Rollback touches the homestead table only; no serving either."""
        for rel in ("internal/firewall/guard.go", "internal/firewall/rollback.go"):
            src = read(rel)
            for banned in ('"flush ruleset"', "net/http", "HandleFunc",
                           "exec.Command", "os.WriteFile"):
                with self.subTest(file=rel, banned=banned):
                    self.assertNotIn(banned, src)


if __name__ == "__main__":
    unittest.main()
