"""S171 — Immich Package conformance tests.

Guardrails for the third package: a curated manifest with pinned
non-latest images and declared requirements, exposure, and backup
paths, internal services carrying no endpoint, loading valid from
the repository catalog alongside earlier packages, and evaluating
cleanly on sufficient hosts. Behavior is covered by Go tests in
internal/catalog loading the shipped directory.
"""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


class TestImmichContract(unittest.TestCase):
    def test_package_exists(self):
        manifest = REPO / "catalog" / "immich.yaml"
        self.assertTrue(manifest.is_file(), "catalog/immich.yaml missing")
        text = manifest.read_text(encoding="utf-8")
        for symbol in ("name: immich", "version:", "immich-server:",
                       "exposes: web", "recommended: local"):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, text)

    def test_no_latest_tag(self):
        text = (REPO / "catalog" / "immich.yaml").read_text(encoding="utf-8")
        self.assertNotIn(":latest", text)


if __name__ == "__main__":
    unittest.main()
