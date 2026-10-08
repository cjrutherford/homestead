"""S151 — Catalog Loader conformance tests.

Guardrails for curated loading: one manifest per package file with
filenames bound to manifest names, strict fields failing typos
loudly, every entry validated, non-manifest files ignored, empty
directories loading empty, unknown packages failing typed, and
local files only with versions surfacing verbatim. Behavior is
covered by Go tests in internal/catalog against temporary
directories.
"""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def read(rel: str) -> str:
    return (REPO / rel).read_text(encoding="utf-8")


class TestLoaderContract(unittest.TestCase):
    def test_loader_shapes(self):
        src = read("internal/catalog/loader.go")
        for symbol in ("func Load(", "func (c Catalog) Get(",
                       "func (c Catalog) Names(",
                       "ErrUnknownPackage", "KnownFields"):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)


if __name__ == "__main__":
    unittest.main()
