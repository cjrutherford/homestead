"""S160 — Jellyfin Catalog Package conformance tests.

Guardrails for the first package: a curated manifest with pinned
non-latest images and declared requirements, exposure, and backup
paths, loading valid from the repository catalog with versions
surfacing verbatim, and evaluating cleanly on sufficient hosts.
Behavior is covered by Go tests in internal/catalog loading the
shipped directory.
"""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


class TestJellyfinContract(unittest.TestCase):
    def test_package_exists(self):
        manifest = REPO / "catalog" / "jellyfin.yaml"
        self.assertTrue(manifest.is_file(), "catalog/jellyfin.yaml missing")
        text = manifest.read_text(encoding="utf-8")
        for symbol in ("name: jellyfin", "version:", "jellyfin/jellyfin:",
                       "exposes: web", "recommended: local"):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, text)

    def test_no_latest_tag(self):
        text = (REPO / "catalog" / "jellyfin.yaml").read_text(encoding="utf-8")
        self.assertNotIn(":latest", text)


if __name__ == "__main__":
    unittest.main()
