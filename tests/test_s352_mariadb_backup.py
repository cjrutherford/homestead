"""S352 — MariaDB/MySQL backup export conformance tests.

Guardrails for the F2 foundation: MariaDB and MySQL join the server
database kinds the catalog can declare, dumps use each engine's own
client with one consistent snapshot and no password in argv, the
password secret resolves for scheduled backups, and password setting or
verification refuses clearly instead of half-working. Behavior is
covered by Go tests in internal/backup and internal/catalog.
"""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def read(rel: str) -> str:
    return (REPO / rel).read_text(encoding="utf-8")


class TestMariaDBBackupContract(unittest.TestCase):
    def setUp(self):
        self.catalog = read("internal/catalog/backup.go")
        self.database = read("internal/backup/database.go")
        self.schedule = read("internal/backup/schedule.go")

    def test_kinds_are_declared(self):
        self.assertIn('DatabaseMariaDB DatabaseKind = "mariadb"', self.catalog)
        self.assertIn('DatabaseMySQL DatabaseKind = "mysql"', self.catalog)
        self.assertIn("func (k DatabaseKind) Server() bool", self.catalog)
        self.assertIn("must be sqlite, postgres, mariadb or mysql", self.catalog)

    def test_dump_uses_each_engine_client(self):
        self.assertIn('"mariadb-dump"', self.database)
        self.assertIn('"mysqldump"', self.database)

    def test_dump_is_consistent_and_non_blocking(self):
        for flag in ("--single-transaction", "--quick",
                     "--skip-lock-tables", "--no-tablespaces"):
            with self.subTest(flag=flag):
                self.assertIn(flag, self.database)
        # MySQL needs its own GTID handling; MariaDB's client rejects it.
        self.assertIn("--set-gtid-purged=OFF", self.database)

    def test_password_never_enters_argv(self):
        self.assertIn("MYSQL_PWD", self.database)
        self.assertIn("IFS= read -r MYSQL_PWD", self.database)
        self.assertNotIn("--password=", self.database)

    def test_dump_connects_over_loopback(self):
        self.assertIn("--host=127.0.0.1", self.database)

    def test_schedules_require_a_password_for_every_server_engine(self):
        self.assertIn("Kind.Server() && j.Password ==", self.schedule)

    def test_restore_is_documented(self):
        runbook = read("docs/architecture/BACKUP_RESTORE_RUNBOOK.md")
        for symbol in ("### MariaDB and MySQL",
                       "```sh runbook mariadb-up",
                       "```sh runbook mariadb-restore",
                       'MYSQL_PWD="$DBPASS"',
                       '"$DBCLIENT"'):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, runbook)

    def test_password_rotation_refuses_clearly(self):
        rotate = read("internal/catalog/rotate.go")
        self.assertIn("not supported yet", rotate)
        self.assertIn("register the new value with --skip-verify", rotate)

    def test_slice_is_recorded(self):
        plan = read("docs/SLICE_PLAN.md")
        self.assertIn("| S352 | MariaDB/MySQL backup export (F2)", plan)
        self.assertIn("S352", read("docs/SLICE_INDEX.md"))
        self.assertIn("S352", read("docs/DEVELOPMENT.md"))


if __name__ == "__main__":
    unittest.main()
