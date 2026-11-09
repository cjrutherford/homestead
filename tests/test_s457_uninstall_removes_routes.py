"""S457 slice recorded and wired."""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def read(*p):
    return (REPO.joinpath(*p)).read_text(encoding="utf-8")


class TestS457(unittest.TestCase):
    def test_slice_recorded(self):
        self.assertIn("S457", read("docs", "SLICE_INDEX.md"))
        self.assertTrue(list((REPO / "docs" / "slices").glob("S457-*.md")))

    def test_wired(self):
        if "S457" == "S456":
            self.assertIn("restorePeople(rt, m, plan, actor, failed)", read("internal", "appsso", "apply.go"))
            self.assertIn("restorePeople(rt, m, plan, actor, failed)", read("internal", "appsso", "forwardauth.go"))
            self.assertIn("RestoreApp", read("internal", "server", "appsso.go"))
        else:
            self.assertIn("remove-edge-routes", read("internal", "server", "catalog.go"))
            self.assertIn("func removeAppRoutes", read("internal", "server", "uninstallguard.go"))


if __name__ == "__main__":
    unittest.main()
