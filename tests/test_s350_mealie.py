"""S350 — Mealie Catalog Package conformance tests.

Guardrails for the GHCR recipe manager: a pinned image tag, the routed
base URL, SQLite on the backed-up data volume, closed signups behind the
upstream-seeded administrator, and no secret or supervisor wiring.
Behavior is covered by Go tests in internal/catalog loading the shipped
directory.
"""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent
MANIFEST = REPO / "catalog" / "mealie.yaml"


class TestMealieContract(unittest.TestCase):
    def setUp(self):
        self.text = MANIFEST.read_text(encoding="utf-8")

    def test_package_exists(self):
        self.assertTrue(MANIFEST.is_file(), "catalog/mealie.yaml missing")
        for symbol in ("name: mealie", "version: 3.29.0",
                       "image: ghcr.io/mealie-recipes/mealie:v3.29.0",
                       "exposes: web", "recommended: local", "allowwider: true"):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, self.text)

    def test_base_url_follows_the_route(self):
        self.assertIn('BASE_URL: "https://{{route_host}}"', self.text)

    def test_sqlite_with_closed_signups(self):
        self.assertIn("DB_ENGINE: sqlite", self.text)
        self.assertIn('ALLOW_SIGNUP: "false"', self.text)

    def test_data_volume_is_declared_and_backed_up(self):
        self.assertIn("target: /app/data", self.text)
        self.assertIn("class: application", self.text)
        self.assertIn("backuppaths:", self.text)

    def test_only_the_sign_in_secret_and_no_entrypoint_override(self):
        # Mealie seeds its administrator and generates its own secrets
        # into the data volume; the only declared secret is the Authentik
        # client secret (S403), read from a file. The image entrypoint
        # runs unchanged.
        self.assertIn("name: oidc_client_secret", self.text)
        self.assertIn("env: OIDC_CLIENT_SECRET_FILE", self.text)
        self.assertIn("adapter: env-oidc", self.text)
        self.assertNotIn("entrypoint:", self.text)

    def test_data_is_owned_by_the_image_user(self):
        # run.sh chowns /app to 911 and drops to it before reading the
        # secret file, so the file must be owned by 911:911 (S405).
        self.assertIn("uid: 911", self.text)
        self.assertIn("gid: 911", self.text)

    def test_no_latest_tag(self):
        self.assertNotIn(":latest", self.text)


if __name__ == "__main__":
    unittest.main()
