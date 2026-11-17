"""S472 - Navidrome at a subpath."""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def read(rel):
    return (REPO / rel).read_text(encoding="utf-8")


class NavidromeSubpath(unittest.TestCase):
    def test_manifest(self):
        m = read("catalog/navidrome.yaml")
        self.assertIn("publish: path", m)
        self.assertIn("serve: path", m)
        self.assertIn("path: /apps/navidrome/", m)
        self.assertIn("ND_BASEURL", m)

    def test_gate_inside_dashboard_site(self):
        g = read("internal/docker/gate.go")
        self.assertIn("renderGatedPath", g)
        self.assertIn("PathBypass", g)
        self.assertIn("PathGateCanonical", g)
        self.assertIn("renderOutpostHandle", read("internal/docker/gate.go"))

    def test_slice_and_checklist(self):
        self.assertTrue((REPO / "docs/slices/S472-navidrome-at-subpath.md").exists())
        c = read("docs/reviews/OWNER_ACCEPTANCE_CHECKLIST.md")
        self.assertIn("SRV-NAVIDROME-2a", c)
        self.assertIn("/apps/navidrome/", c)


if __name__ == "__main__":
    unittest.main()
