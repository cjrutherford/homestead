"""S354 — Navidrome catalog package conformance tests.

Guardrails for the second Wave 2 package: a pinned official image, SQLite
on application storage with a consistent export, the regenerable cache on
bulk, the owner music library left to the install, the documented local-auth
exception, and the shared-library declaration. Behavior is covered by Go
tests in internal/catalog loading the shipped directory.
"""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent
MANIFEST = REPO / "catalog" / "navidrome.yaml"


class TestNavidromeContract(unittest.TestCase):
    def setUp(self):
        self.text = MANIFEST.read_text(encoding="utf-8")

    def test_package_exists(self):
        self.assertTrue(MANIFEST.is_file(), "catalog/navidrome.yaml missing")
        for symbol in ("name: navidrome", "version: 0.64.2",
                       "image: docker.io/navidrome/navidrome:0.64.2",
                       "exposes: web", "ports: [4533]", "recommended: local"):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, self.text)

    def test_data_and_backup_paths(self):
        for symbol in ("target: /data", "target: /cache",
                       "subpath: data", "subpath: cache",
                       "ND_CACHEFOLDER: /cache", "backuppaths:", "- data"):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, self.text)

    def test_music_library_is_an_owner_volume(self):
        # /music is an image VOLUME, but the catalog leaves it to the
        # install so the shared library never enters a backup.
        self.assertNotIn("target: /music", self.text)
        self.assertIn("owner volume", self.text)

    def test_sqlite_export(self):
        self.assertIn("kind: sqlite", self.text)
        self.assertIn("path: data/navidrome.db", self.text)

    def test_documented_local_auth_exception(self):
        self.assertIn("class: unsupported", self.text)
        self.assertNotIn("\nsecrets:", self.text)

    def test_shared_library_integration(self):
        self.assertIn("kind: library", self.text)
        self.assertIn("target: jellyfin", self.text)

    def test_no_latest_tag(self):
        self.assertNotIn(":latest", self.text)


if __name__ == "__main__":
    unittest.main()
