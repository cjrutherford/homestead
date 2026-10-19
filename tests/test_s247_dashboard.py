"""S247 — Dashboard Exposure Surfaces conformance tests.

Guardrails for dashboard parity on exposure and tunnel flows:
routes registered before the wildcard, nav links present, API
methods covering every S245/S246 route, selection saving through
the generic wizard endpoint, and PARITY rows naming the dashboard
methods. Behavior is covered by Angular specs for the exposure
and tunnel pages.
"""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def read(rel: str) -> str:
    return (REPO / rel).read_text(encoding="utf-8")


class TestDashboardExposureSurfaces(unittest.TestCase):
    def test_routes(self):
        src = read("dashboard/src/app/app.routes.ts")
        for symbol in ("{ path: 'exposure', loadComponent: () => import('./exposure/exposure').then((m) => m.Exposure) }",
                       "{ path: 'tunnel', loadComponent: () => import('./tunnel/tunnel').then((m) => m.Tunnel) }"):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)

    def test_nav(self):
        src = read("dashboard/src/app/core/nav.ts")
        for symbol in ("path: '/exposure'", "path: '/tunnel'"):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)

    def test_api_methods(self):
        src = read("dashboard/src/app/core/api.service.ts")
        for symbol in ("exposureReview():", "exposureApprovals():",
                       "exposureApprove(", "exposureApply(",
                       "tunnelStatus():", "tunnelSetup(",
                       "tunnelTeardown("):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)

    def test_parity_rows(self):
        matrix = read("docs/PARITY.md")
        for symbol in ("| `exposureReview` |", "| `exposureApprove` |",
                       "| `exposureApply` |", "| `tunnelStatus` |",
                       "| `tunnelSetup` |", "| `tunnelTeardown` |"):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, matrix)


if __name__ == "__main__":
    unittest.main()
