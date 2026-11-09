"""S167 — Jellyfin Angular Install conformance tests.

Guardrails for the dashboard flow: catalog list, detail, plan,
install, and configure routes serving validated manifests with
server-assembled host facts, long executions returning operation
IDs onto existing operation streams, dashboard service methods plus
page plus route plus nav present, and parity rows naming dashboard
symbols with CLI explicitly arriving next slice. Behavior is
covered by Go tests in internal/server and Angular specs.
"""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def read(rel: str) -> str:
    return (REPO / rel).read_text(encoding="utf-8")


class TestAngularInstallContract(unittest.TestCase):
    def test_api_shapes(self):
        src = read("internal/server/catalog.go")
        for symbol in ("func (s *Server) MountCatalog(",
                       "/api/v1/catalog",
                       "func hostFacts(",
                       "operation.NewRunner"):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)

    def test_dashboard_shapes(self):
        service = read("dashboard/src/app/core/api.service.ts")
        for symbol in ("catalogPackages", "catalogPackage",
                       "planInstall", "installPackage",
                       "configurePackage"):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, service)
        routes = read("dashboard/src/app/app.routes.ts")
        self.assertIn("catalog", routes)
        nav = read("dashboard/src/app/core/nav.ts")
        self.assertIn("path: '/admin/catalog'", nav)

    def test_parity_rows(self):
        parity = read("docs/PARITY.md")
        for symbol in ("/api/v1/catalog", "catalogPackages",
                       "catalogPackage", "planInstall",
                       "installPackage", "configurePackage"):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, parity)


if __name__ == "__main__":
    unittest.main()
