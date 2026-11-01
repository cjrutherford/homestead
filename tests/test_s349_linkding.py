"""S349 — Linkding Catalog Package conformance tests.

Guardrails for the smallest Wave 1 package: one pinned container, the
image CMD named as the startup the supervisor execs, a superuser password
that travels as a file and is exported rather than sitting in plain
environment, a routed CSRF origin, and SQLite on application storage.
Behavior is covered by Go tests in internal/catalog loading the shipped
directory.
"""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent
MANIFEST = REPO / "catalog" / "linkding.yaml"


class TestLinkdingContract(unittest.TestCase):
    def setUp(self):
        self.text = MANIFEST.read_text(encoding="utf-8")

    def test_package_exists(self):
        self.assertTrue(MANIFEST.is_file(), "catalog/linkding.yaml missing")
        for symbol in ("name: linkding", "version: 1.47.0",
                       "image: docker.io/sissbruecker/linkding:1.47.0",
                       "exposes: web", "recommended: local", "allowwider: true"):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, self.text)

    def test_startup_is_the_image_command(self):
        # No ENTRYPOINT in the image: ./bootstrap.sh is its CMD, and the
        # supervisor must exec exactly that script.
        self.assertIn('entrypoint: ["./bootstrap.sh"]', self.text)
        self.assertNotIn("\n  command:", self.text)

    def test_superuser_password_is_exported_not_plain(self):
        self.assertIn("LD_SUPERUSER_NAME: admin", self.text)
        self.assertIn("LD_SUPERUSER_PASSWORD_FILE", self.text)
        self.assertIn("export: true", self.text)
        self.assertNotIn("LD_SUPERUSER_PASSWORD:", self.text)

    def test_csrf_origin_follows_the_route(self):
        self.assertIn('LD_CSRF_TRUSTED_ORIGINS: "{{route_url}},{{app_url}}"',
                      self.text)

    def test_data_lives_on_application_storage(self):
        self.assertIn("target: /etc/linkding/data", self.text)
        self.assertIn("class: application", self.text)
        self.assertIn("backuppaths:", self.text)

    def test_no_latest_tag(self):
        self.assertNotIn(":latest", self.text)


if __name__ == "__main__":
    unittest.main()
