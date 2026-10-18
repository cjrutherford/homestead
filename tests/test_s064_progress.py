"""S064 — Operation Progress UI conformance tests.

Guardrails for the reusable progress surface: newest-first list plus a
detail page that fetches once and follows the live stream until
terminal, over the S062 endpoints, with the list endpoint, CLI list
command, and dashboard pages all present. Rich display stays plain and
honest; producers still arrive with their own slices. Behavior is
covered by Go tests, Vitest specs, and the S037 parity matrix (which
gains the list row here).
"""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent
CTL = REPO / "cmd" / "homesteadctl"
APP = REPO / "dashboard" / "src" / "app"


def read(rel: str) -> str:
    return (REPO / rel).read_text(encoding="utf-8")


def read_dash(rel: str) -> str:
    return (REPO / "dashboard" / rel).read_text(encoding="utf-8")


def ctl_sources() -> str:
    return "\n".join(
        p.read_text(encoding="utf-8") for p in CTL.glob("*.go") if not p.name.endswith("_test.go")
    )


class TestProgressSurface(unittest.TestCase):
    def test_list_endpoint(self):
        src = read("internal/server/operations.go")
        self.assertIn("GET /api/v1/operations", src)
        self.assertIn("handleOperationList", src)

    def test_client_and_cli_list(self):
        self.assertIn("func (c *Client) ListOperations(", read("internal/apiclient/operations.go"))
        src = ctl_sources()
        for cmd in ("runOperationsList", '"operations"'):
            with self.subTest(cmd=cmd):
                self.assertIn(cmd, src)

    def test_pages_exist(self):
        for name in ("operations/operations.ts", "operations/operations.html",
                     "operations/operations.spec.ts", "operations/operation-detail.ts",
                     "operations/operation-detail.html", "operations/operation-detail.spec.ts"):
            with self.subTest(name=name):
                self.assertTrue((APP / name).is_file())

    def test_list_and_detail_flow(self):
        src = read_dash("src/app/operations/operations.ts")
        self.assertIn("operations()", src)
        detail = read_dash("src/app/operations/operation-detail.ts")
        self.assertIn("operationEvents(", detail)
        self.assertIn("takeUntilDestroyed", detail)
        self.assertIn("paramMap", detail)

    def test_routes_and_nav(self):
        routes = read_dash("src/app/app.routes.ts")
        self.assertIn("path: 'operations'", routes)
        self.assertIn("path: 'operations/:id'", routes)
        html = read_dash("src/app/core/nav.ts")
        self.assertIn("path: '/operations'", html)

    def test_parity_row(self):
        parity = read("docs/PARITY.md")
        self.assertIn("| GET | /api/v1/operations |", parity)


if __name__ == "__main__":
    unittest.main()
