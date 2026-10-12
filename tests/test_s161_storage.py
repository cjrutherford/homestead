"""S161 — Jellyfin Storage conformance tests.

S276 replaced the bespoke internal/jellyfin storage derivation with the
generic catalog placement; these guardrails now pin that path.
Guardrails for library mapping: configuration under the application
assignment (provisioned) with media referenced under bulk (never
created), every required class assigned, libraries existing as
directories inside bulk with unique names, and installer volumes
plus provision-only-config outputs. Behavior is covered by Go tests
in internal/catalog against the curated manifest and temporary
filesystems.
"""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def read(rel: str) -> str:
    return (REPO / rel).read_text(encoding="utf-8")


class TestJellyfinStorageContract(unittest.TestCase):
    def test_storage_shapes(self):
        src = read("internal/catalog/install.go")
        for symbol in ("func validateHostPath(", "HostPath", "outside the",
                       "is not a directory", "never creates them"):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)
        data = read("internal/catalog/data.go")
        for symbol in ("func ResolveData(", "func nestedStubs("):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, data)
        self.assertFalse((REPO / "internal/jellyfin/storage.go").exists())


if __name__ == "__main__":
    unittest.main()
