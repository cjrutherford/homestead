"""S013 — SQLite Operational State conformance tests.

Guardrails for the versioned store: the driver stays pure-Go (static daemon
per ADR-001), the database defaults under the S012 state directory, the four
goal domains have tables, connection pragmas are enforced, and migrations
are versioned and recorded. Runtime behavior is covered by Go tests in
internal/store.
"""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def read(rel: str) -> str:
    return (REPO / rel).read_text(encoding="utf-8")


class TestDriverChoice(unittest.TestCase):
    def test_pure_go_driver(self):
        """ADR-001 self-contained static daemon: no cgo SQLite driver."""
        gomod = read("go.mod")
        self.assertIn("modernc.org/sqlite", gomod)
        self.assertNotIn("mattn/go-sqlite3", gomod)

    def test_store_imports_no_cgo_driver(self):
        src = read("internal/store/store.go")
        self.assertIn("modernc.org/sqlite", src)


class TestStoreContract(unittest.TestCase):
    def test_default_path_under_lib_dir(self):
        src = read("internal/store/store.go")
        self.assertIn("layout.LibDir", src)
        self.assertIn("homestead.db", src)

    def test_four_domain_tables_created(self):
        src = read("internal/store/store.go")
        for table in ("operations", "identities", "sessions", "onboarding"):
            with self.subTest(table=table):
                self.assertIn(f"CREATE TABLE IF NOT EXISTS {table}", src)

    def test_pragmas_enforced(self):
        src = read("internal/store/store.go")
        for pragma in ("foreign_keys = ON", "journal_mode = WAL", "busy_timeout"):
            with self.subTest(pragma=pragma):
                self.assertIn(pragma, src)

    def test_migrations_versioned_and_recorded(self):
        src = read("internal/store/store.go")
        self.assertIn("schema_migrations", src)
        self.assertIn("CurrentVersion", src)
        self.assertIn("MaxOpenConns(1)", src)


if __name__ == "__main__":
    unittest.main()
