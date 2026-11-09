"""S462 - Doctor reports a service stopped cleanly by hand."""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent
LIVE = (REPO / "internal" / "server" / "doctorlive.go").read_text(encoding="utf-8")


class TestDoctorStopped(unittest.TestCase):
    def test_exit_zero_is_not_excused(self):
        probe = LIVE[LIVE.index("func probeAppContainers"):LIVE.index("func probeAppReach")]
        self.assertNotIn('"Exited (0)"', probe)

    def test_slice_recorded(self):
        index = (REPO / "docs" / "SLICE_INDEX.md").read_text(encoding="utf-8")
        self.assertIn("S462", index)
        self.assertTrue(list((REPO / "docs" / "slices").glob("S462-*.md")))


if __name__ == "__main__":
    unittest.main()
