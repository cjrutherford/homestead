"""S170 — Nextcloud Package conformance tests.

Guardrails for the second package: a curated manifest with pinned
non-latest images and declared requirements, exposure, and backup
paths, loading valid from the repository catalog alongside the
first package, with SQLite documented as the default database.
Behavior is covered by Go tests in internal/catalog loading the
shipped directory.
"""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


class TestNextcloudContract(unittest.TestCase):
    def test_package_exists(self):
        manifest = REPO / "catalog" / "nextcloud.yaml"
        self.assertTrue(manifest.is_file(), "catalog/nextcloud.yaml missing")
        text = manifest.read_text(encoding="utf-8")
        for symbol in ("name: nextcloud", "version:", "nextcloud:",
                       "exposes: web", "recommended: local"):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, text)

    def test_no_latest_tag(self):
        text = (REPO / "catalog" / "nextcloud.yaml").read_text(encoding="utf-8")
        self.assertNotIn(":latest", text)


if __name__ == "__main__":
    unittest.main()
