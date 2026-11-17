"""S346 — Paperless-ngx Catalog Package conformance tests.

Guardrails for the first three-service package: a pinned GHCR image that
runs s6's /init, Postgres metadata, a Valkey queue, all four image
VOLUMEs declared, supervised secret export for an image with no *_FILE
input, a routed HTTPS URL, and a local-first exposure default. Behavior
is covered by Go tests in internal/catalog loading the shipped directory.
"""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent
MANIFEST = REPO / "catalog" / "paperless-ngx.yaml"


class TestPaperlessContract(unittest.TestCase):
    def setUp(self):
        self.text = MANIFEST.read_text(encoding="utf-8")

    def test_package_exists(self):
        self.assertTrue(MANIFEST.is_file(), "catalog/paperless-ngx.yaml missing")
        for symbol in ("name: paperless-ngx", "version: 3.3.4",
                       "ghcr.io/paperless-ngx/paperless-ngx:3.3.4",
                       "exposes: web", "recommended: local", "allowwider: true"):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, self.text)

    def test_three_services_with_pinned_data_stores(self):
        for image in ("docker.io/library/postgres:18",
                      "docker.io/valkey/valkey:9-alpine"):
            with self.subTest(image=image):
                self.assertIn(f"image: {image}", self.text)
        self.assertEqual(self.text.count("\n  image: "), 3)

    def test_entrypoint_is_the_image_init_for_supervised_export(self):
        self.assertIn('entrypoint: ["/init"]', self.text)
        # The supervisor execs the image entrypoint; a command override
        # would replace the s6 services and start nothing.
        self.assertNotIn("\n  command:", self.text)

    def test_every_image_volume_is_declared(self):
        # The Go suite checks declarations against the image VOLUME table;
        # this pins the four paths the v3.3.4 image declares.
        for path in ("/usr/src/paperless/data", "/usr/src/paperless/media",
                     "/usr/src/paperless/consume", "/usr/src/paperless/export"):
            with self.subTest(path=path):
                self.assertIn(f"target: {path}", self.text)
        self.assertIn("database: true", self.text)

    def test_secrets_are_exported_not_plaintext(self):
        for env in ("PAPERLESS_DBPASS_FILE", "PAPERLESS_SECRET_KEY_FILE",
                    "PAPERLESS_ADMIN_PASSWORD_FILE"):
            with self.subTest(env=env):
                self.assertIn(f"env: {env}", self.text)
                self.assertIn("export: true", self.text)
        self.assertNotIn("PAPERLESS_DBPASS:", self.text)
        self.assertNotIn("PAPERLESS_SECRET_KEY:", self.text)
        self.assertNotIn("PAPERLESS_ADMIN_PASSWORD:", self.text)

    def test_sign_in_secret_is_a_derived_file_not_env(self):
        # S413: the provider JSON contains the client secret, so it is a
        # rendered secret file; the manifest's plain env never holds it.
        self.assertIn("env: PAPERLESS_SOCIALACCOUNT_PROVIDERS_FILE", self.text)
        self.assertIn('initial: "{}"', self.text)
        self.assertIn("{{client_secret}}", self.text)
        self.assertNotIn("PAPERLESS_DISABLE_REGULAR_LOGIN:", self.text)
        self.assertNotIn("PAPERLESS_SOCIALACCOUNT_PROVIDERS:", self.text)

    def test_url_follows_the_route_and_ocr_is_set(self):
        self.assertIn('PAPERLESS_URL: "https://{{route_host}}"', self.text)
        self.assertIn("PAPERLESS_OCR_LANGUAGE: eng", self.text)

    def test_no_latest_tag(self):
        self.assertNotIn(":latest", self.text)


if __name__ == "__main__":
    unittest.main()
