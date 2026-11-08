"""S453 - Paperless-ngx is usable from install: new sign-ins join a group
with working permissions, local sign-up is closed, and the local admin
password is revealable."""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent
MANIFEST = (REPO / "catalog" / "paperless-ngx.yaml").read_text(encoding="utf-8")
ROUTINE = (REPO / "internal" / "firstrun" / "paperless.go").read_text(encoding="utf-8")


class TestPaperlessDefaultGroup(unittest.TestCase):
    def test_manifest_names_group_and_closes_signup(self):
        for text in ('PAPERLESS_SOCIAL_ACCOUNT_DEFAULT_GROUPS: Homestead people',
                     'PAPERLESS_ACCOUNT_ALLOW_SIGNUPS: "false"',
                     "routine: paperless-ngx", "secret: admin_password", "reveal: true"):
            with self.subTest(text=text):
                self.assertIn(text, MANIFEST)

    def test_routine_creates_the_same_group_without_passwords(self):
        self.assertIn('PaperlessPeopleGroup = "Homestead people"', ROUTINE)
        self.assertIn("view_uisettings", ROUTINE)
        self.assertNotIn("set_password", ROUTINE)

    def test_slice_recorded(self):
        index = (REPO / "docs" / "SLICE_INDEX.md").read_text(encoding="utf-8")
        self.assertIn("S453", index)
        self.assertTrue(list((REPO / "docs" / "slices").glob("S453-*.md")))


if __name__ == "__main__":
    unittest.main()
