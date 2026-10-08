"""S193 — Update UI conformance tests.

The review API serves per-package drift plans with backup gates,
approved updates start safe-update operations with no-op reports
for current packages, and the dashboard reviews plans with
backup-gated apply onto operation streams. CLI rows wait for a
later updates CLI slice. Behavior is covered by Go tests in
internal/server with a faked engine and by dashboard specs.
"""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def read(rel: str) -> str:
    return (REPO / rel).read_text(encoding="utf-8")


class TestUpdateUI(unittest.TestCase):
    def test_review_api_shapes(self):
        src = read("internal/server/updates.go")
        for symbol in ("MountUpdates",
                       "GET /api/v1/updates",
                       "POST /api/v1/updates/{name}/apply",
                       "already current",
                       "backup required"):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)

    def test_dashboard_shapes(self):
        component = read("dashboard/src/app/updates/updates.ts")
        for symbol in ("updates()", "applyUpdate(", "db_password"):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, component)
        template = read("dashboard/src/app/updates/updates.html")
        for symbol in ("Apply update", "Back up before updating",
                       "operations"):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, template)

    def test_parity_rows(self):
        matrix = read("docs/PARITY.md")
        for symbol in ("/api/v1/updates",
                       "/api/v1/updates/{name}/apply",
                       "`updates`",
                       "`applyUpdate`"):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, matrix)


if __name__ == "__main__":
    unittest.main()
