"""S480 - the shared Authentik gate for apps at a subpath (ADR-062)."""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def read(rel):
    return (REPO / rel).read_text(encoding="utf-8")


class SharedGate(unittest.TestCase):
    def test_adr_and_index(self):
        adr = read("docs/adr/ADR-062-shared-subpath-gate.md")
        self.assertIn("Accepted", adr)
        for word in ("X-Authentik-Groups", "exact", "outpost", "Navidrome", "403", "LAN"):
            self.assertIn(word, adr)
        self.assertIn("ADR-062", read("docs/adr/README.md"))

    def test_slice_plan(self):
        plan = read("docs/SLICE_PLAN.md")
        for s in ("S480", "S481", "S482"):
            self.assertIn("| " + s + " |", plan)

    def test_edge_group_check(self):
        g = read("internal/docker/gate.go")
        self.assertIn("header_regexp", g)
        self.assertIn("SharedGateCanonical", g)
        self.assertIn("GroupsHeader", g)
        self.assertIn(r"(^|\|)(", g)

    def test_shared_provider(self):
        f = read("internal/appsso/forwardauth.go")
        self.assertIn("SharedGateProvider", f)
        self.assertIn("retire-own-provider", f)
        self.assertIn("RemoveAccess", f)

    def test_slice_doc_index_and_checklist(self):
        self.assertTrue((REPO / "docs/slices/S480-shared-subpath-gate.md").exists())
        self.assertIn("S480 puts every gated app", read("docs/SLICE_INDEX.md"))
        self.assertIn("ADR-062", read("docs/reviews/OWNER_ACCEPTANCE_CHECKLIST.md"))


if __name__ == "__main__":
    unittest.main()
