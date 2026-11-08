"""S461 - reset-mfa decodes the daemon's device counts."""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent
CLIENT = (REPO / "internal" / "apiclient" / "people.go").read_text(encoding="utf-8")
SERVICE = (REPO / "dashboard" / "src" / "app" / "people" / "people.service.ts").read_text(encoding="utf-8")


class TestResetMFACounts(unittest.TestCase):
    def test_client_counts(self):
        self.assertIn("Removed int `json:\"removed\"`", CLIENT)
        self.assertIn("Skipped int `json:\"skipped\"`", CLIENT)

    def test_dashboard_counts(self):
        self.assertIn("{ removed: number; skipped: number }", SERVICE)

    def test_slice_recorded(self):
        index = (REPO / "docs" / "SLICE_INDEX.md").read_text(encoding="utf-8")
        self.assertIn("S461", index)
        self.assertTrue(list((REPO / "docs" / "slices").glob("S461-*.md")))


if __name__ == "__main__":
    unittest.main()
