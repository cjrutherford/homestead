"""S459 - applying Nextcloud sign-in turns the welcome wizard app off."""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent
ADAPTERS = (REPO / "internal" / "appsso" / "adapters.go").read_text(encoding="utf-8")


class TestNextcloudNoWizard(unittest.TestCase):
    def test_enable_disables_firstrunwizard(self):
        self.assertIn('{"app:disable", "firstrunwizard"}', ADAPTERS)

    def test_slice_recorded(self):
        index = (REPO / "docs" / "SLICE_INDEX.md").read_text(encoding="utf-8")
        self.assertIn("S459", index)
        self.assertTrue(list((REPO / "docs" / "slices").glob("S459-*.md")))


if __name__ == "__main__":
    unittest.main()
