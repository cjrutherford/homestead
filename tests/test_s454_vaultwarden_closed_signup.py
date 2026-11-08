"""S454 - Vaultwarden: local registration closed, Authentik sign-in creates
the account, the groups claim carries email_verified."""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent
MANIFEST = (REPO / "catalog" / "vaultwarden.yaml").read_text(encoding="utf-8")


class TestVaultwardenClosedSignup(unittest.TestCase):
    def test_registration_closed_and_admin_off(self):
        self.assertIn('SIGNUPS_ALLOWED: "false"', MANIFEST)
        self.assertNotIn("ADMIN_TOKEN:", MANIFEST)
        self.assertNotIn("SSO_ONLY", MANIFEST.replace("SSO_ONLY is", ""))

    def test_email_verified_and_groups_scope(self):
        self.assertIn("verifiedemail: true", MANIFEST)
        self.assertIn('SSO_SCOPES: "email profile groups"', MANIFEST)

    def test_schema_field_and_adapter(self):
        schema = (REPO / "internal" / "catalog" / "schema.go").read_text(encoding="utf-8")
        adapter = (REPO / "internal" / "appsso" / "envoidc.go").read_text(encoding="utf-8")
        self.assertIn('yaml:"verifiedemail', schema)
        self.assertIn('"email_verified": "True"', adapter)

    def test_slice_recorded(self):
        index = (REPO / "docs" / "SLICE_INDEX.md").read_text(encoding="utf-8")
        self.assertIn("S454", index)
        self.assertTrue(list((REPO / "docs" / "slices").glob("S454-*.md")))


if __name__ == "__main__":
    unittest.main()
