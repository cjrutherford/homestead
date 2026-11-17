"""S357 — Vikunja Catalog Package conformance tests.

Guardrails: pinned image, no secret in the environment, the sign-in config
file mounted by secret path, and local login left alone. Behavior is covered
by Go tests in internal/appsso and internal/catalog.
"""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent
MANIFEST = REPO / "catalog" / "vikunja.yaml"


class TestVikunjaContract(unittest.TestCase):
    def setUp(self):
        self.text = MANIFEST.read_text(encoding="utf-8")

    def test_package_exists(self):
        for symbol in ("name: vikunja", "version: 2.7.1",
                       "image: docker.io/vikunja/vikunja:2.7.1",
                       "containerport: 3456", "exposes: web", "recommended: local"):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, self.text)

    def test_no_latest_and_no_supervisor(self):
        self.assertNotIn(":latest", self.text)
        self.assertNotIn("entrypoint:", self.text)
        self.assertNotIn("export: true", self.text)

    def test_public_url_follows_the_route_and_sqlite_is_on_data(self):
        self.assertIn('VIKUNJA_SERVICE_PUBLICURL: "{{app_url}}/"', self.text)
        self.assertIn("VIKUNJA_DATABASE_TYPE: sqlite", self.text)
        self.assertIn("target: /db", self.text)
        self.assertIn("target: /app/vikunja/files", self.text)

    def test_sign_in_uses_a_mounted_config_file(self):
        for symbol in ("adapter: env-oidc", "path: /etc/vikunja/config.yml",
                       "initial: \"{}\"", "/auth/openid/authentik",
                       "trust: SSL_CERT_FILE", "trustbundle: combined",
                       "VIKUNJA_AUTH_OPENID_ENABLED"):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, self.text)

    def test_local_login_is_left_on_and_secret_not_in_env(self):
        self.assertNotIn("VIKUNJA_AUTH_LOCAL_ENABLED", self.text)
        self.assertNotIn("VIKUNJA_AUTH_OPENID_PROVIDERS", self.text)
        self.assertNotIn("CLIENTSECRET", self.text)


if __name__ == "__main__":
    unittest.main()
