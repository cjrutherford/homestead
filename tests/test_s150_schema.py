"""S150 — Catalog Schema conformance tests.

Guardrails for curated manifests: pinned non-latest images with
services linked to declared endpoints, semantic versions, bounded UX
copy, memory/storage/port requirements with unique classes and
ports, exposure declarations bound to the manifest name, contained
relative backup paths, and invalid manifests refused throughout.
Behavior is covered by Go tests in internal/catalog.
"""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def read(rel: str) -> str:
    return (REPO / rel).read_text(encoding="utf-8")


class TestSchemaContract(unittest.TestCase):
    def test_schema_shapes(self):
        src = read("internal/catalog/schema.go")
        for symbol in ("type Manifest", "type Service",
                       "type Requirements", "type StorageNeed",
                       "func (m Manifest) Validate()",
                       "non-latest tag", "must equal manifest"):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)


if __name__ == "__main__":
    unittest.main()
