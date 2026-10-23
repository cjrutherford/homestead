"""S348 — FreshRSS Catalog Package conformance tests.

Guardrails for the Debian-based feed reader: the OIDC-capable image (not
the alpine variant), scheduled background refresh, both writable
directories on application storage as www-data, a web-installer setup
that declares no secret, and a local-first exposure default. Behavior is
covered by Go tests in internal/catalog loading the shipped directory.
"""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent
MANIFEST = REPO / "catalog" / "freshrss.yaml"


class TestFreshRSSContract(unittest.TestCase):
    def setUp(self):
        self.text = MANIFEST.read_text(encoding="utf-8")

    def test_package_exists(self):
        self.assertTrue(MANIFEST.is_file(), "catalog/freshrss.yaml missing")
        for symbol in ("name: freshrss", "version: 1.30.4",
                       "image: docker.io/freshrss/freshrss:1.30.4",
                       "exposes: web", "recommended: local", "allowwider: true"):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, self.text)

    def test_debian_image_for_oidc(self):
        # The -alpine variant lacks mod_auth_openidc, so the SSO
        # declaration only holds for the plain (Debian) tag.
        self.assertNotIn("image: docker.io/freshrss/freshrss:1.30.4-alpine",
                         self.text)
        self.assertIn("sso:", self.text)

    def test_background_refresh_is_scheduled(self):
        self.assertIn('CRON_MIN: "*/20"', self.text)
        self.assertIn("LISTEN: 0.0.0.0:80", self.text)

    def test_writable_directories_are_declared(self):
        for path in ("/var/www/FreshRSS/data", "/var/www/FreshRSS/extensions"):
            with self.subTest(path=path):
                self.assertIn(f"target: {path}", self.text)
        self.assertIn("backuppaths:", self.text)

    def test_no_generated_credentials(self):
        # The owner completes the web installer; nothing is declared or
        # exported, so no supervisor entrypoint is needed.
        self.assertNotIn("\nsecrets:", self.text)
        self.assertNotIn("entrypoint:", self.text)
        self.assertNotIn("FRESHRSS_USER:", self.text)

    def test_no_latest_tag(self):
        self.assertNotIn(":latest", self.text)


if __name__ == "__main__":
    unittest.main()
