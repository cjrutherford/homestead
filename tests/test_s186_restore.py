"""S186 — Restore Execution conformance tests.

Guardrails for approved restoration: per-entry copies with mirror
semantics plus closing verification comparing file sets and sizes,
orphaned live files not surviving, empty and shape-broken plans
refused before anything runs, verification mismatches aborting,
and runner-backed execution failing fast. Behavior is covered by
Go tests in internal/backup against seeded runs.
"""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def read(rel: str) -> str:
    return (REPO / rel).read_text(encoding="utf-8")


class TestRestoreExecuteContract(unittest.TestCase):
    def test_execute_shapes(self):
        src = read("internal/backup/execute.go")
        for symbol in ("func RestoreDefinition(",
                       '"backup.restore"', "verify"):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)


if __name__ == "__main__":
    unittest.main()
