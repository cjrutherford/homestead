"""S351 — Uptime Kuma Catalog Package conformance tests.

Guardrails for the documented SSO and exposure exception: a pinned image,
SQLite on application storage, the owner-created account, no secret or
supervisor wiring, and an exposure posture that recommends local and
refuses widening so it can never become a public status page. Behavior is
covered by Go tests in internal/catalog loading the shipped directory.
"""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent
MANIFEST = REPO / "catalog" / "uptime-kuma.yaml"


class TestUptimeKumaContract(unittest.TestCase):
    def setUp(self):
        self.text = MANIFEST.read_text(encoding="utf-8")

    def test_package_exists(self):
        self.assertTrue(MANIFEST.is_file(), "catalog/uptime-kuma.yaml missing")
        for symbol in ("name: uptime-kuma", "version: 2.5.7",
                       "image: docker.io/louislam/uptime-kuma:2.5.7",
                       "exposes: web", "recommended: local"):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, self.text)

    def test_exposure_refuses_to_widen(self):
        self.assertIn("allowwider: false", self.text)
        self.assertIn("public status page", self.text)
        self.assertNotIn("allowwider: true", self.text)

    def test_documented_sso_exception(self):
        self.assertIn("class: unsupported", self.text)

    def test_data_volume_is_declared_and_backed_up(self):
        self.assertIn("target: /app/data", self.text)
        self.assertIn("class: application", self.text)
        self.assertIn("backuppaths:", self.text)

    def test_no_secret_or_entrypoint_override(self):
        # The administrator account is created in the app on first visit;
        # the image entrypoint runs unchanged.
        self.assertNotIn("\nsecrets:", self.text)
        self.assertNotIn("entrypoint:", self.text)

    def test_no_latest_tag(self):
        self.assertNotIn(":latest", self.text)


if __name__ == "__main__":
    unittest.main()
