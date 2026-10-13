"""S237 — Proxy Serve Surfaces conformance tests.

Guardrails for edge operations over the API and CLI: Caddy
deployment and route-table reconciliation run through the
operation engine, invalid tables and malformed bodies refuse
before running anything, route files take capitalized keys
matching the docker model, and the CLI carries both with
follow support. Behavior is covered by Go tests in
internal/server and cmd/homesteadctl.
"""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def read(rel: str) -> str:
    return (REPO / rel).read_text(encoding="utf-8")


class TestEdgeSurfaces(unittest.TestCase):
    def test_api_shapes(self):
        src = read("internal/server/edge.go")
        for symbol in ("POST /api/v1/edge/deploy",
                       "POST /api/v1/edge/routes",
                       "GET /api/v1/edge/routes",
                       "func (s *Server) MountEdge(",
                       "DeployDefinition(", "ReconcileDefinition("):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)

    def test_cli_shapes(self):
        src = read("cmd/homesteadctl/edge.go")
        for symbol in ('"deploy"', '"routes"', '"set"',
                       "EdgeDeploy(", "EdgeReconcile(",
                       "--follow", "loadRoutesFile"):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)

    def test_parity_rows(self):
        matrix = read("docs/PARITY.md")
        for symbol in ("| /api/v1/edge/deploy |",
                       "| /api/v1/edge/routes |",
                       "`\"edge\"`"):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, matrix)


if __name__ == "__main__":
    unittest.main()
