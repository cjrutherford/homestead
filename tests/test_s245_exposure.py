"""S245 — Exposure Surfaces conformance tests.

Guardrails for exposure review, approval, and application over
the API and CLI: review recomputes from saved inputs with
missing selections reading as absent, approvals name the actor
with explicit CIDRs and record per host, application needs an
approval and runs firewall composition plus edge write through
the operation engine, and malformed bodies refuse. Behavior is
covered by Go tests in internal/server and cmd/homesteadctl.
"""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def read(rel: str) -> str:
    return (REPO / rel).read_text(encoding="utf-8")


class TestExposureSurfaces(unittest.TestCase):
    def test_api_shapes(self):
        src = read("internal/server/exposure.go")
        for symbol in ("GET /api/v1/exposure/review",
                       "GET /api/v1/exposure/approvals",
                       "POST /api/v1/exposure/approvals",
                       "POST /api/v1/exposure/apply",
                       "func (s *Server) MountExposure("):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)

    def test_cli_shapes(self):
        src = read("cmd/homesteadctl/exposure.go")
        for symbol in ('"review"', '"approvals"', '"approve"',
                       '"apply"', "--actor", "--network",
                       "--follow"):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)

    def test_parity_rows(self):
        matrix = read("docs/PARITY.md")
        for symbol in ("| /api/v1/exposure/review |",
                       "| /api/v1/exposure/approvals |",
                       "| /api/v1/exposure/apply |"):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, matrix)


if __name__ == "__main__":
    unittest.main()
