"""S168 — Jellyfin CLI Install conformance tests.

Guardrails for the CLI flow: a generic apps group over the same
endpoints (list, reviewed plan, approved install with optional
follow, service configuration by flags), file-driven install bodies
matching the dashboard shape, shared API client methods, and parity
rows naming the group on every catalog route. Behavior is covered
by Go tests in cmd/homesteadctl and internal/apiclient against
scripted daemons.
"""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def read(rel: str) -> str:
    return (REPO / rel).read_text(encoding="utf-8")


class TestCLIInstallContract(unittest.TestCase):
    def test_cli_shapes(self):
        src = read("cmd/homesteadctl/apps.go")
        for symbol in ("homesteadctl apps <list|plan|install|configure>",
                       "apps plan <package>", "apps install",
                       "apps configure", "--follow", "--set",
                       "--unset"):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)

    def test_client_shapes(self):
        src = read("internal/apiclient/catalog.go")
        for symbol in ("func (c *Client) CatalogPackages(",
                       "func (c *Client) CatalogPackage(",
                       "func (c *Client) PlanInstall(",
                       "func (c *Client) InstallPackage(",
                       "func (c *Client) ConfigurePackage("):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)

    def test_parity_complete(self):
        parity = read("docs/PARITY.md")
        for route in ("/api/v1/catalog", "/api/v1/catalog/{name}",
                      "/api/v1/catalog/{name}/plan",
                      "/api/v1/catalog/{name}/install",
                      "/api/v1/catalog/{name}/configure"):
            with self.subTest(route=route):
                self.assertIn(route, parity)
        self.assertNotIn("arrives in S168", parity)


if __name__ == "__main__":
    unittest.main()
