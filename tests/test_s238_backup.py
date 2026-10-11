"""S238 — Backup Configuration Surfaces conformance tests.

Guardrails for backup setup over the API and CLI: destination
resolution names absence instead of guessing, schedules persist
with hourly floors and actor presence, malformed bodies and short
intervals refuse, deletes converge idempotently, and lists read
empty (never null). Behavior is covered by Go tests in
internal/server and cmd/homesteadctl.
"""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def read(rel: str) -> str:
    return (REPO / rel).read_text(encoding="utf-8")


class TestBackupSurfaces(unittest.TestCase):
    def test_api_shapes(self):
        src = read("internal/server/backup.go")
        for symbol in ("GET /api/v1/backups/destination",
                       "GET /api/v1/backups/schedules",
                       "POST /api/v1/backups/schedules",
                       "DELETE /api/v1/backups/schedules/{package}",
                       "func (s *Server) MountBackups("):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)

    def test_cli_shapes(self):
        src = read("cmd/homesteadctl/backup.go")
        for symbol in ('"destination"', '"schedules"', '"schedule"',
                       '"unschedule"', "--interval", "--retain",
                       "--actor", "--package"):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)

    def test_parity_rows(self):
        matrix = read("docs/PARITY.md")
        for symbol in ("| /api/v1/backups/destination |",
                       "| /api/v1/backups/schedules |",
                       "| /api/v1/backups/schedules/{package} |",
                       '`\"backup\"`'):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, matrix)


if __name__ == "__main__":
    unittest.main()
