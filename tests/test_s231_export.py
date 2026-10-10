"""S231 — Configuration Export conformance tests.

Guardrails for portable configuration: the export bundles desired
state with provenance metadata and secret names only — values are
structurally unreachable (only List is ever called), documents stay
0644-safe and inspectable YAML with strict parsing and version
bounds, writes are atomic, and import builds on the same reader.
Behavior is covered by Go tests in internal/desired.
"""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def read(rel: str) -> str:
    return (REPO / rel).read_text(encoding="utf-8")


class TestConfigExport(unittest.TestCase):
    def test_export_shapes(self):
        src = read("internal/desired/export.go")
        for symbol in ("type Export", "ExportVersion",
                       "func ExportState(", "func WriteExport(",
                       "func ReadExport(", "Secrets", "secrets.List()"):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)

    def test_values_unreachable(self):
        src = read("internal/desired/export.go")
        self.assertIn("secrets.List()", src)
        for banned in ("secrets.Get(", "s.open(", "unseal"):
            with self.subTest(banned=banned):
                self.assertNotIn(banned, src)


if __name__ == "__main__":
    unittest.main()
