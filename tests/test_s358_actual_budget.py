"""S358 — Actual Budget Catalog Package conformance tests.

Guardrails: pinned image, native OpenID by environment, the client secret
only as a file, no plain secret variable. Behavior is covered by Go tests
in internal/appsso and internal/catalog.
"""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent
MANIFEST = REPO / "catalog" / "actual-budget.yaml"


class TestActualBudgetContract(unittest.TestCase):
    def setUp(self):
        self.text = MANIFEST.read_text(encoding="utf-8")

    def test_package_exists(self):
        for symbol in ("name: actual-budget", "version: 26.10.0",
                       "image: docker.io/actualbudget/actual-server:26.10.0",
                       "containerport: 5006", "exposes: web", "recommended: local",
                       "target: /data", "icon: mdi:piggy-bank-outline"):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, self.text)

    def test_no_latest_and_no_supervisor(self):
        self.assertNotIn(":latest", self.text)
        self.assertNotIn("entrypoint:", self.text)
        self.assertNotIn("export: true", self.text)
        self.assertNotIn("serve: path", self.text)

    def test_sign_in_is_native_openid_with_secret_file(self):
        for symbol in ("adapter: env-oidc", "/openid/callback",
                       "ACTUAL_OPENID_DISCOVERY_URL", "ACTUAL_OPENID_CLIENT_ID",
                       "ACTUAL_OPENID_CLIENT_SECRET_FILE",
                       "ACTUAL_OPENID_SERVER_HOSTNAME: \"{{app_url}}\"",
                       "ACTUAL_USER_CREATION_MODE: login",
                       "trust: NODE_EXTRA_CA_CERTS", "const VERIFIED = '26.10.0'"):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, self.text)

    def test_secret_not_plain_and_password_login_not_enforced(self):
        self.assertNotRegex(self.text, r"ACTUAL_OPENID_CLIENT_SECRET[^_]")
        self.assertNotIn("ACTUAL_OPENID_ENFORCE", self.text)
        self.assertNotIn("exclusive:", self.text)


if __name__ == "__main__":
    unittest.main()
