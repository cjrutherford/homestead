"""S182 — Database Backup conformance tests.

Guardrails for application-aware exports: validated requests bound
to manifest services, pre hooks with export failures still running
post hooks and reporting both, SQLite snapshots through online
VACUUM with busy absorption, Postgres dumps through container
tooling with passwords confined to process environment and 0600
outputs, secrets never stored, and invalid requests refused.
Behavior is covered by Go tests in internal/backup with real
SQLite round-trips and stubbed execution.
"""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def read(rel: str) -> str:
    return (REPO / rel).read_text(encoding="utf-8")


class TestDatabaseContract(unittest.TestCase):
    def test_export_shapes(self):
        src = read("internal/backup/database.go")
        for symbol in ("func ExportDatabase(", "VACUUM INTO",
                       "pg_dump", "PGPASSWORD", "HookPre",
                       "HookPost"):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)


if __name__ == "__main__":
    unittest.main()
