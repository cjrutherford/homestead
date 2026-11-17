"""S499 - trimmed groups claim for every OIDC app; Komga denial note."""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def read(rel):
    return (REPO / rel).read_text(encoding="utf-8")


class TrimEveryApp(unittest.TestCase):
    def test_flag_removed(self):
        self.assertNotIn("trimgroups", read("catalog/freshrss.yaml"))
        self.assertNotIn("TrimGroups", read("internal/catalog/schema.go"))
        self.assertNotIn("TrimGroups", read("internal/appsso/access.go"))

    def test_every_app_answers_to_profile(self):
        self.assertIn('ClaimScope: "profile"', read("internal/appsso/access.go"))
        self.assertIn("TestEveryOIDCAppTrimsGroupsToItsOwn", read("internal/appsso/trimgroups_all_test.go"))

    def test_deferred(self):
        text = read("docs/DEFERRED.md")
        self.assertNotIn("S496: only FreshRSS is trimmed", text)
        self.assertIn("S499: every OIDC app's claim", text)

    def test_docs(self):
        self.assertIn("S499", read("docs/SLICE_INDEX.md"))
        self.assertIn("Permission denied", read("docs/guide/troubleshooting.md"))
        self.assertIn("popup", read("docs/reviews/OWNER_ACCEPTANCE_CHECKLIST.md"))
        self.assertIn("Permission denied", read("docs/slices/S499-trim-groups-every-app.md"))


if __name__ == "__main__":
    unittest.main()
