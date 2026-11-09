"""S481 - the 13 craft apps at /apps/<app>/ behind the shared gate (ADR-062)."""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent
CRAFT = ["cadcraft", "deckcraft", "designcraft", "effectcraft", "figcraft", "filmcraft", "gridcraft",
         "lightcraft", "pdfcraft", "photocraft", "soundcraft", "vectorcraft", "wordcraft"]


def read(rel):
    return (REPO / rel).read_text(encoding="utf-8")


class CraftAtSubpath(unittest.TestCase):
    def test_manifests(self):
        for name in CRAFT:
            text = read(f"catalog/{name}.yaml")
            self.assertIn("publish: path", text, name)
            self.assertNotIn("publish: never", text, name)
            self.assertIn("allowwider: true", text, name)
            self.assertIn(f"forwardauth:\n    service: {name}\n", text, name)
            self.assertIn("serve: path", text, name)
            self.assertIn("strip_prefix: true", text, name)

    def test_tunnel_only_gate_and_fail_closed(self):
        g = read("internal/docker/gate.go")
        self.assertIn("TunnelOnly", g)
        r = read("internal/docker/routes.go")
        self.assertIn("craftUpstream", r)
        self.assertIn("p.Gate.TunnelOnly", r)
        self.assertIn("tunnel && p.CraftApp != \"\" && p.Gate == nil", r)

    def test_owner_decision_recorded(self):
        adr = read("docs/adr/ADR-062-shared-subpath-gate.md")
        self.assertIn("keep\nthe LAN open", adr)
        self.assertNotIn("Open question for the owner", adr)
        self.assertIn("Resolved in S481", read("docs/DEFERRED.md"))

    def test_slice_doc_index_and_checklist(self):
        self.assertTrue((REPO / "docs/slices/S481-craft-apps-at-subpath.md").exists())
        self.assertIn("S481 publishes the 13 craft apps", read("docs/SLICE_INDEX.md"))
        cl = read("docs/reviews/OWNER_ACCEPTANCE_CHECKLIST.md")
        self.assertEqual(cl.count("Public path is behind the Authentik gate"), 13)
        self.assertNotIn("Not reachable publicly", cl)


if __name__ == "__main__":
    unittest.main()
