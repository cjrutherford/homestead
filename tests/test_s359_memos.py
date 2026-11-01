"""S359 — Memos Catalog Package conformance tests.

Guardrails: pinned image, Authentik sign-in through Memos's declarative
configuration file mounted as a secret, no secret in the environment.
Behavior is covered by Go tests in internal/appsso and internal/catalog.
"""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent
MANIFEST = REPO / "catalog" / "memos.yaml"


class TestMemosContract(unittest.TestCase):
    def setUp(self):
        self.text = MANIFEST.read_text(encoding="utf-8")

    def test_package_exists(self):
        for symbol in ("name: memos", "version: 26.10.0",
                       "image: docker.io/neosmemo/memos:26.10.0",
                       "containerport: 5230", "exposes: web", "recommended: local",
                       "target: /var/opt/memos", "uid: 10001",
                       "icon: mdi:notebook-edit-outline"):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, self.text)

    def test_no_latest_and_no_path_serving(self):
        self.assertNotIn(":latest", self.text)
        self.assertNotIn("serve: path", self.text)
        self.assertNotIn("export: true", self.text)

    def test_sign_in_is_a_mounted_configuration_file(self):
        for symbol in ("adapter: env-oidc", "/auth/callback",
                       "path: /etc/memos/config/homestead.json",
                       "identity-providers/authentik",
                       "{{authentik_base}}/application/o/authorize/",
                       "{{authentik_base}}/application/o/token/",
                       "{{authentik_base}}/application/o/userinfo/",
                       "trust: SSL_CERT_FILE", "trustbundle: combined",
                       '"destroy":true'):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, self.text)

    def test_no_script_no_db_write_and_local_login_kept(self):
        for symbol in ("onapply:", "onoff:", "sqlite3", "exclusive:",
                       "disallowPasswordAuth", "disallowUserRegistration"):
            with self.subTest(symbol=symbol):
                self.assertNotIn(symbol, self.text)


if __name__ == "__main__":
    unittest.main()
