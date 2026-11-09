"""S496 - FreshRSS owner sign-in: trimmed groups claim."""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def read(rel):
    return (REPO / rel).read_text(encoding="utf-8")


class FreshRSSTrim(unittest.TestCase):
    def test_manifest_and_schema(self):
        self.assertIn("trimgroups: true", read("catalog/freshrss.yaml"))
        self.assertIn('yaml:"trimgroups,omitempty"', read("internal/catalog/schema.go"))

    def test_claim_scope(self):
        self.assertIn('ClaimScope = "profile"', read("internal/appsso/access.go").replace("x.ClaimScope", "ClaimScope"))
        self.assertIn('"Homestead: "', read("internal/sso/authentik.go"))

    def test_deferred_entry_replaced(self):
        text = read("docs/DEFERRED.md")
        self.assertNotIn("S482: FreshRSS sign-in fails for the owner", text)
        self.assertIn("S496: only FreshRSS is trimmed", text)

    def test_docs(self):
        self.assertIn("16400", read("docs/slices/S496-freshrss-owner-sign-in.md"))
        self.assertIn("S496 fixes", read("docs/SLICE_INDEX.md"))
        self.assertIn("Fixed in S496", read("docs/guide/troubleshooting.md"))
        self.assertIn("S496", read("docs/reviews/OWNER_ACCEPTANCE_CHECKLIST.md"))


if __name__ == "__main__":
    unittest.main()
