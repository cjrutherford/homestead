"""S345 — Vaultwarden Catalog Package conformance tests.

Guardrails for the first Wave 1 package: owner signup without an
administrative surface, a domain that follows the routed host, local
exposure with a widened-access warning, and a pinned image tag.
Behavior is covered by Go tests in internal/catalog loading the shipped
directory.
"""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


class TestVaultwardenContract(unittest.TestCase):
    def test_package_exists(self):
        manifest = REPO / "catalog" / "vaultwarden.yaml"
        self.assertTrue(manifest.is_file(), "catalog/vaultwarden.yaml missing")
        text = manifest.read_text(encoding="utf-8")
        for symbol in ("name: vaultwarden", "version: 1.37.4",
                       "vaultwarden/server:1.37.4", "exposes: web",
                       "recommended: local", "allowwider: true"):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, text)

    def test_owner_signup_without_admin_surface(self):
        text = (REPO / "catalog" / "vaultwarden.yaml").read_text(encoding="utf-8")
        self.assertIn('SIGNUPS_ALLOWED: "true"', text)
        self.assertNotIn("ADMIN_TOKEN:", text,
                         "the admin page must stay off by default")

    def test_domain_follows_the_route(self):
        text = (REPO / "catalog" / "vaultwarden.yaml").read_text(encoding="utf-8")
        self.assertIn('DOMAIN: "{{app_url}}"', text)

    def test_no_latest_tag(self):
        text = (REPO / "catalog" / "vaultwarden.yaml").read_text(encoding="utf-8")
        self.assertNotIn(":latest", text)


if __name__ == "__main__":
    unittest.main()
