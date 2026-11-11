"""S498 - published path apps redirect the LAN to the public address."""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def read(rel):
    return (REPO / rel).read_text(encoding="utf-8")


class LANRedirect(unittest.TestCase):
    def test_edge_rule(self):
        src = read("internal/docker/routes.go")
        self.assertIn("redirectsLANPath", src)
        self.assertIn("r.CraftApp == \"\"", src)
        self.assertTrue((REPO / "internal/docker/lanredirect_test.go").exists())

    def test_adr_and_guide(self):
        self.assertIn("Amendment (S498", read("docs/adr/ADR-062-shared-subpath-gate.md"))
        self.assertIn("307", read("docs/guide/exposure.md"))
        self.assertIn("S498", read("docs/SLICE_INDEX.md"))
        self.assertIn("S498:", read("docs/DEFERRED.md"))
        self.assertIn("(S498)", read("docs/reviews/OWNER_ACCEPTANCE_CHECKLIST.md"))
        self.assertIn("redirect", read("docs/slices/S498-published-path-lan-redirect.md"))


if __name__ == "__main__":
    unittest.main()
