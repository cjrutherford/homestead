"""S355 — Komga catalog package conformance tests.

Guardrails for the third Wave 2 package: a pinned official image, a JVM
heap cap inside the declared memory, SQLite on application storage with a
consistent export, the comics library left to the install, optional OIDC,
and the identity integration. Behavior is covered by Go tests in
internal/catalog loading the shipped directory.
"""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent
MANIFEST = REPO / "catalog" / "komga.yaml"


class TestKomgaContract(unittest.TestCase):
    def setUp(self):
        self.text = MANIFEST.read_text(encoding="utf-8")

    def test_package_exists(self):
        self.assertTrue(MANIFEST.is_file(), "catalog/komga.yaml missing")
        for symbol in ("name: komga", "version: 1.29.1",
                       "image: docker.io/gotson/komga:1.29.1",
                       "exposes: web", "containerport: 25600", "recommended: local"):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, self.text)

    def test_jvm_heap_is_capped(self):
        self.assertIn("JAVA_TOOL_OPTIONS: -Xmx768m", self.text)
        self.assertIn("memorymb: 1024", self.text)

    def test_data_and_backup_paths(self):
        for symbol in ("target: /config", "subpath: config", "class: application",
                       "backuppaths:", "- config"):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, self.text)

    def test_comics_library_is_an_owner_volume(self):
        # The library is a bulk owner volume: declaring /data would put
        # the collection under <bulk>/komga and copy it into every backup.
        self.assertNotIn("target: /data", self.text)
        self.assertIn("owner volume", self.text)

    def test_sqlite_export(self):
        self.assertIn("kind: sqlite", self.text)
        self.assertIn("path: config/database.sqlite", self.text)

    def test_optional_oidc_and_identity(self):
        self.assertIn("class: optional", self.text)
        self.assertIn("protocols: [oidc]", self.text)
        self.assertIn("kind: identity", self.text)
        self.assertIn("target: authentik", self.text)
        # S392: a recovery password is generated only for a fresh /config.
        self.assertIn("firstrun:\n  routine: komga", self.text)

    def test_no_latest_tag(self):
        self.assertNotIn(":latest", self.text)


if __name__ == "__main__":
    unittest.main()
