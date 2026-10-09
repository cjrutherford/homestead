"""S211 — Advanced Administration Link conformance tests.

Guardrails for the expert link: the Dockge route constructor
hard-codes local exposure with internal TLS so the engine-control
interface cannot describe anything wider, the engine assessment
proves the posture aligned and finding-free, the API serves the
link read-only with installed status from engine labels, and the
dashboard Advanced page states the policy beside the link without
changing anything. Behavior is covered by Go tests in
internal/docker and internal/server plus dashboard specs.
"""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def read(rel: str) -> str:
    return (REPO / rel).read_text(encoding="utf-8")


class TestAdvancedLink(unittest.TestCase):
    def test_route_model(self):
        src = read("internal/docker/dockge.go")
        for symbol in ("func DockgeRouteFor(",
                       "Exposure: ExposureLocal",
                       "func DockgeInstalled("):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)

    def test_link_api(self):
        src = read("internal/server/dockge.go")
        for symbol in ("GET /api/v1/dockge",
                       "func (s *Server) MountDockge(",
                       "Aligned", "Installed"):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)

    def test_dashboard_surface(self):
        component = read("dashboard/src/app/advanced/advanced.ts")
        for symbol in ("dockge(", "DockgeLink"):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, component)
        template = read("dashboard/src/app/advanced/advanced.html")
        for symbol in ("entry.url", "entry.exposure", "entry.installed"):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, template)

    def test_parity_row(self):
        matrix = read("docs/PARITY.md")
        for symbol in ("| /api/v1/dockge |", "`dockge`"):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, matrix)


if __name__ == "__main__":
    unittest.main()
