"""S483 - config export and import in the dashboard."""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def read(rel):
    return (REPO / rel).read_text(encoding="utf-8")


class ConfigTransfer(unittest.TestCase):
    def test_docs(self):
        self.assertIn("config.import", read("docs/slices/S483-config-export-import-dashboard.md"))
        self.assertIn("S483 adds the dashboard surface", read("docs/SLICE_INDEX.md"))

    def test_parity_rows(self):
        text = read("docs/PARITY.md")
        for symbol in ("configExportYaml", "previewConfigImport", "importConfig"):
            self.assertIn(f"`{symbol}`", text)
        self.assertIn("/api/v1/config/import/preview", text)

    def test_server_and_cli(self):
        self.assertIn("config/import/preview", read("internal/server/configio.go"))
        self.assertIn("config.import", read("internal/server/configio.go"))
        self.assertIn("--dry-run", read("docs/slices/S483-config-export-import-dashboard.md"))
        self.assertIn('"dry-run"', read("cmd/homesteadctl/config.go"))

    def test_dashboard_never_stores_secret_values(self):
        ts = read("dashboard/src/app/config/config-transfer.ts")
        self.assertNotIn("localStorage", ts)
        self.assertNotIn("sessionStorage", ts)
        self.assertIn('type="password"', read("dashboard/src/app/config/config-transfer.html"))
        self.assertIn("app-config-transfer", read("dashboard/src/app/config/config.html"))

    def test_checklist_and_deferred(self):
        text = read("docs/reviews/OWNER_ACCEPTANCE_CHECKLIST.md")
        self.assertIn("**END-PARITY-2 ", text)
        self.assertIn("**END-PARITY-3 ", text)
        self.assertIn("| END-PARITY | END-PARITY-1 to END-PARITY-3 (3) |", text)
        self.assertIn("END-PARITY-3", read("docs/reviews/acceptance-form.html"))
        self.assertIn("S483:", read("docs/DEFERRED.md"))


if __name__ == "__main__":
    unittest.main()
