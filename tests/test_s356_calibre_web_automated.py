"""S356 — Calibre-Web Automated catalog package conformance tests.

Guardrails for the fourth Wave 2 package: a pinned community image, the
LinuxServer user the declared directories match, the library and shared
ingest folder on bulk storage, the application databases with a consistent
export, optional in-app OIDC, and the identity and shared-library
declarations. Behavior is covered by Go tests in internal/catalog loading
the shipped directory.
"""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent
MANIFEST = REPO / "catalog" / "calibre-web-automated.yaml"


class TestCalibreWebAutomatedContract(unittest.TestCase):
    def setUp(self):
        self.text = MANIFEST.read_text(encoding="utf-8")

    def test_package_exists(self):
        self.assertTrue(MANIFEST.is_file(), "catalog/calibre-web-automated.yaml missing")
        for symbol in ("name: calibre-web-automated", "version: 4.0.8",
                       "image: docker.io/crocodilestick/calibre-web-automated:v4.0.8",
                       "exposes: web", "ports: [8083]", "recommended: local"):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, self.text)

    def test_runs_as_the_declared_user(self):
        for symbol in ('PUID: "1000"', 'PGID: "1000"', "uid: 1000", "gid: 1000"):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, self.text)

    def test_data_placement(self):
        for symbol in ("target: /config", "subpath: config", "class: application",
                       "target: /calibre-library", "subpath: library",
                       "target: /cwa-book-ingest", "subpath: ingest"):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, self.text)

    def test_backup_paths_cover_the_library_not_the_transient_ingest(self):
        self.assertIn("backuppaths:\n- config\n- library", self.text)
        # The ingest folder is transient: the service removes files after
        # filing them, so it is declared but not a backup path.
        self.assertNotIn("- ingest", self.text)

    def test_sqlite_export(self):
        self.assertIn("kind: sqlite", self.text)
        self.assertIn("path: config/app.db", self.text)

    def test_optional_oidc_and_integrations(self):
        self.assertIn("class: optional", self.text)
        self.assertIn("protocols: [oidc]", self.text)
        self.assertIn("kind: identity", self.text)
        self.assertIn("target: authentik", self.text)
        self.assertIn("kind: library", self.text)
        self.assertIn("target: nextcloud", self.text)
        self.assertNotIn("\nsecrets:", self.text)

    def test_no_latest_tag(self):
        self.assertNotIn(":latest", self.text)


if __name__ == "__main__":
    unittest.main()
