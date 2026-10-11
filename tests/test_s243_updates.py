"""S243 — Updates CLI conformance tests.

Guardrails for reviewing and applying updates through the CLI:
the review lists installed, current, and backup-gated packages
with moves, warnings, and reasons; apply carries backup opt-in
with a database password file, reports no-ops without opening
operations, streams with follow, and refuses usage-first on bad
invocations. Behavior is covered by Go tests in
cmd/homesteadctl over the updates API.
"""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def read(rel: str) -> str:
    return (REPO / rel).read_text(encoding="utf-8")


class TestUpdatesCLI(unittest.TestCase):
    def test_cli_shapes(self):
        src = read("cmd/homesteadctl/updates.go")
        for symbol in ('"list"', '"apply"',
                       "--backup", "--db-password-file", "--follow",
                       "ApplyUpdate(", "UpdatesList("):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)

    def test_client_shapes(self):
        src = read("internal/apiclient/updates.go")
        for symbol in ("type PackageUpdate", "type UpdatePlan",
                       "func (c *Client) UpdatesList(",
                       "func (c *Client) ApplyUpdate("):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)

    def test_parity_rows(self):
        matrix = read("docs/PARITY.md")
        for symbol in ("| /api/v1/updates |", "| /api/v1/updates/{name}/apply |"):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, matrix)
        rows = [line for line in matrix.splitlines() if "/api/v1/updates" in line]
        for row in rows:
            with self.subTest(row=row):
                cells = [cell.strip() for cell in row.strip("|").split("|")]
                for cell in cells[2:4]:
                    self.assertNotIn("n/a", cell)


if __name__ == "__main__":
    unittest.main()
