"""S236 — App Removal Surfaces conformance tests.

Guardrails for uninstalling through the API and CLI: unknown
packages 404, missing stacks refuse before running anything,
data removal without explicit roots refuses, removal runs through
the operation engine with follow support, and data-dir bodies
take MOUNT:PATH shape. Behavior is covered by Go tests in
internal/server and cmd/homesteadctl.
"""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def read(rel: str) -> str:
    return (REPO / rel).read_text(encoding="utf-8")


class TestRemovalSurfaces(unittest.TestCase):
    def test_api_shapes(self):
        src = read("internal/server/catalog.go")
        for symbol in ("POST /api/v1/catalog/{name}/uninstall",
                       "func handleCatalogUninstall(",
                       "RemoveDefinition(", "stackDirFor"):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)

    def test_cli_shapes(self):
        src = read("cmd/homesteadctl/apps.go")
        for symbol in ('"uninstall"', "runAppsUninstall",
                       "--remove-data", "--data-dir", "--follow",
                       "UninstallPackage("):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)

    def test_parity_row(self):
        matrix = read("docs/PARITY.md")
        self.assertIn("| /api/v1/catalog/{name}/uninstall |", matrix)


if __name__ == "__main__":
    unittest.main()
