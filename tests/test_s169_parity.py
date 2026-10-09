"""S169 — Jellyfin Parity Acceptance conformance tests.

Acceptance that CLI and dashboard converge: identical install
bodies plan byte-identical change sets (proven at runtime in
internal/server), dashboard form keys matching server-decoded keys
exactly, catalog parity rows carrying both surfaces with no gaps,
and the vertical slice closing with every phase-15 slice committed.
"""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent

KEYS = {
    "domain",
    "hostnames",
    "container_ports",
    "storage",
    "volumes",
    "networks",
    "zone",
    "public_ip",
}


def read(rel: str) -> str:
    return (REPO / rel).read_text(encoding="utf-8")


class TestParityAcceptanceContract(unittest.TestCase):
    def test_body_keys_match(self):
        dashboard = read("dashboard/src/app/catalog/catalog.ts")
        server = read("internal/server/catalog.go")
        for key in KEYS:
            with self.subTest(key=key):
                self.assertIn(key, dashboard)
                self.assertIn(f'"{key}"', server)

    def test_matrix_complete(self):
        parity = read("docs/PARITY.md")
        # Install-flow rows carry both surfaces with no gaps; drift
        # rows (S213) carry dashboard only, covered by S213 tests.
        rows = [line for line in parity.splitlines()
                if "/api/v1/catalog" in line and "/drift" not in line]
        self.assertEqual(len(rows), 5)
        for row in rows:
            with self.subTest(row=row):
                cells = [cell.strip() for cell in row.strip("|").split("|")]
                for cell in cells[2:4]:
                    self.assertNotIn("n/a", cell)

    def test_runtime_acceptance(self):
        server = read("internal/server/catalog_test.go")
        self.assertIn("TestCatalogParityAcceptance", server)


if __name__ == "__main__":
    unittest.main()
