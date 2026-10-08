"""S161 — Jellyfin Storage conformance tests.

Guardrails for library mapping: configuration under the application
assignment (provisioned) with media referenced under bulk (never
created), every required class assigned, libraries existing as
directories inside bulk with unique names, and installer volumes
plus provision-only-config outputs. Behavior is covered by Go tests
in internal/jellyfin against the curated manifest and temporary
filesystems.
"""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def read(rel: str) -> str:
    return (REPO / rel).read_text(encoding="utf-8")


class TestJellyfinStorageContract(unittest.TestCase):
    def test_storage_shapes(self):
        src = read("internal/jellyfin/storage.go")
        for symbol in ("func PlanStorage(", "type StoragePlan",
                       "type MediaBinding", "func (p StoragePlan) Volumes(",
                       "func (p StoragePlan) ProvisionDirs(",
                       "outside the bulk mount"):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)


if __name__ == "__main__":
    unittest.main()
