"""S496 - FreshRSS owner sign-in: trimmed groups claim."""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def read(rel):
    return (REPO / rel).read_text(encoding="utf-8")


class FreshRSSTrim(unittest.TestCase):
    def test_manifest_and_schema(self):
        # S499 removed the opt-in flag: every app is trimmed.
        self.assertNotIn("trimgroups", read("catalog/freshrss.yaml"))

    def test_claim_scope(self):
        self.assertIn('ClaimScope: "profile"', read("internal/appsso/access.go"))
        self.assertIn('"Homestead: "', read("internal/sso/authentik.go"))

    def test_deferred_entry_replaced(self):
        text = read("docs/DEFERRED.md")
        pass
        self.assertIn("S499: every OIDC app's claim", text)

    def test_docs(self):
        self.assertIn("16400", read("docs/slices/S496-freshrss-owner-sign-in.md"))
        self.assertIn("S496 fixes", read("docs/SLICE_INDEX.md"))
        self.assertIn("Fixed in S496", read("docs/guide/troubleshooting.md"))
        self.assertIn("S496", read("docs/reviews/OWNER_ACCEPTANCE_CHECKLIST.md"))


if __name__ == "__main__":
    unittest.main()
