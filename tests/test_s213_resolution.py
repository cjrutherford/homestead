"""S213 — Drift Resolution conformance tests.

Guardrails for the choices: recorded content pairs both sides for
review, accept adopts current files as managed, restore rewrites
recorded bytes with generated modes and converges through an
operation, clean stacks report no-op instead of empty operations,
and unknown actions refuse. Behavior is covered by Go tests in
internal/catalog and internal/server plus dashboard specs.
"""

import re
import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def read(rel: str) -> str:
    return (REPO / rel).read_text(encoding="utf-8")


class TestDriftResolution(unittest.TestCase):
    def test_resolution_shapes(self):
        src = read("internal/catalog/drift.go")
        for symbol in ("type FileReview", "func Review(",
                       "func Accept(", "func Restore(",
                       "func RestoreDefinition(", '"catalog.restore"',
                       "func StoredContents("):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)

    def test_api_shapes(self):
        src = read("internal/server/drift.go")
        for symbol in ("GET /api/v1/catalog/{name}/drift",
                       "POST /api/v1/catalog/{name}/drift",
                       "func (s *Server) MountDrift(",
                       '"accept"', '"restore"', '"no drift"'):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)

    def test_dashboard_surface(self):
        component = read("dashboard/src/app/drift/drift.ts")
        for symbol in ("driftReview(", "driftResolve("):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, component)

    def test_parity_rows(self):
        matrix = read("docs/PARITY.md")
        for symbol in ("| /api/v1/catalog/{name}/drift |",
                       "`driftReview`", "`driftResolve`"):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, matrix)

    def test_schema_v15(self):
        src = read("internal/store/store.go")
        version = int(re.search(r"(?m)^const CurrentVersion = (\d+)$", src).group(1))
        self.assertGreaterEqual(version, 15)
        self.assertIn("ADD COLUMN content", src)


if __name__ == "__main__":
    unittest.main()
