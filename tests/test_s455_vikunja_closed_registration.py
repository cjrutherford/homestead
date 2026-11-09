"""S455 - Vikunja: local registration closed, Authentik sign-in creates accounts."""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent
MANIFEST = (REPO / "catalog" / "vikunja.yaml").read_text(encoding="utf-8")
CHECKLIST = (REPO / "docs" / "reviews" / "OWNER_ACCEPTANCE_CHECKLIST.md").read_text(encoding="utf-8")


class TestVikunjaClosedRegistration(unittest.TestCase):
    def test_registration_closed(self):
        self.assertIn('VIKUNJA_SERVICE_ENABLEREGISTRATION: "false"', MANIFEST)
        self.assertNotIn("registration stay open", MANIFEST)

    def test_checklist_section_does_not_mention_vaultwarden(self):
        a = CHECKLIST.index("### SRV-VIKUNJA: Vikunja")
        b = CHECKLIST.index("## Platform checks with apps installed")
        self.assertNotIn("Vaultwarden", CHECKLIST[a:b])
        self.assertIn("SRV-VIKUNJA-1a", CHECKLIST[a:b])

    def test_slice_recorded(self):
        index = (REPO / "docs" / "SLICE_INDEX.md").read_text(encoding="utf-8")
        self.assertIn("S455", index)
        self.assertTrue(list((REPO / "docs" / "slices").glob("S455-*.md")))


if __name__ == "__main__":
    unittest.main()
