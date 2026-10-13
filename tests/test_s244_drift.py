"""S244 — Drift CLI conformance tests.

Guardrails for drift choices through the CLI: review prints both
sides per drifted file, accept adopts with per-path confirmation,
restore reports no-ops without opening operations and streams
with follow, and bad invocations refuse usage-first. Behavior is
covered by Go tests in cmd/homesteadctl over the drift API.
"""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def read(rel: str) -> str:
    return (REPO / rel).read_text(encoding="utf-8")


class TestDriftCLI(unittest.TestCase):
    def test_cli_shapes(self):
        src = read("cmd/homesteadctl/drift.go")
        for symbol in ('"review"', '"accept"', '"restore"',
                       "--follow", "DriftReview(", "DriftResolve("):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)

    def test_client_shapes(self):
        src = read("internal/apiclient/drift.go")
        for symbol in ("type FileDrift", "type DriftResolution",
                       "func (c *Client) DriftReview(",
                       "func (c *Client) DriftResolve("):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)

    def test_parity_rows(self):
        matrix = read("docs/PARITY.md")
        rows = [line for line in matrix.splitlines() if "/catalog/{name}/drift" in line]
        self.assertEqual(len(rows), 3)  # review, live (S262), resolve
        for row in rows:
            with self.subTest(row=row):
                cells = [cell.strip() for cell in row.strip("|").split("|")]
                for cell in cells[2:4]:
                    self.assertNotIn("n/a", cell)


if __name__ == "__main__":
    unittest.main()
