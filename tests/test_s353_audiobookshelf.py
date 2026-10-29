"""S353 — Audiobookshelf catalog package conformance tests.

Guardrails for the first Wave 2 package: a pinned multi-arch GHCR image,
SQLite on application storage with a consistent export, item metadata on
application storage, transcode streams on bulk, owner media libraries,
optional in-app OIDC, and the shared-library declaration. Behavior is
covered by Go tests in internal/catalog loading the shipped directory.
"""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent
MANIFEST = REPO / "catalog" / "audiobookshelf.yaml"


class TestAudiobookshelfContract(unittest.TestCase):
    def setUp(self):
        self.text = MANIFEST.read_text(encoding="utf-8")

    def test_package_exists(self):
        self.assertTrue(MANIFEST.is_file(), "catalog/audiobookshelf.yaml missing")
        for symbol in ("name: audiobookshelf", "version: 2.37.1",
                       "image: ghcr.io/advplyr/audiobookshelf:2.37.1",
                       "exposes: web", "containerport: 80", "recommended: local"):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, self.text)

    def test_data_and_backup_paths(self):
        for symbol in ("target: /config", "target: /metadata",
                       "target: /metadata/streams", "subpath: config",
                       "subpath: metadata", "subpath: streams",
                       "backuppaths:", "- config", "- metadata"):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, self.text)

    def test_sqlite_export(self):
        self.assertIn("kind: sqlite", self.text)
        self.assertIn("path: config/absdatabase.sqlite", self.text)

    def test_media_libraries_stay_owner_volumes(self):
        # The bulk library is an owner volume: declaring /audiobooks or
        # /podcasts would put the media inside the package directory and
        # copy it into every backup.
        self.assertNotIn("target: /audiobooks", self.text)
        self.assertNotIn("target: /podcasts", self.text)
        self.assertIn("owner volumes", self.text)

    def test_optional_oidc_without_actuated_client(self):
        self.assertIn("class: optional", self.text)
        self.assertIn("protocols: [oidc]", self.text)
        self.assertNotIn("\noidc:", self.text)

    def test_identity_and_library_integrations(self):
        self.assertIn("kind: identity", self.text)
        self.assertIn("target: authentik", self.text)
        self.assertIn("kind: library", self.text)
        self.assertIn("target: nextcloud", self.text)

    def test_no_secret_or_entrypoint_override(self):
        # The JWT signing secret is generated into the backed-up SQLite
        # database; the image entrypoint runs unchanged.
        self.assertNotIn("\nsecrets:", self.text)
        self.assertNotIn("entrypoint:", self.text)

    def test_no_latest_tag(self):
        self.assertNotIn(":latest", self.text)


if __name__ == "__main__":
    unittest.main()
