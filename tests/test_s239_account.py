"""S239 — Account Surfaces conformance tests.

Guardrails for self-service account operations over the API and
CLI: rotation proves the current credential and revokes earlier
sessions, weak replacements and throttled callers get their own
statuses, lockout status answers for the session owner only, and
unknown sessions never reach either. Behavior is covered by Go
tests in internal/server and cmd/homesteadctl.
"""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def read(rel: str) -> str:
    return (REPO / rel).read_text(encoding="utf-8")


class TestAccountSurfaces(unittest.TestCase):
    def test_api_shapes(self):
        src = read("internal/server/auth.go")
        for symbol in ("POST /api/v1/admins/password",
                       "GET /api/v1/admins/lockout",
                       "func (s *Server) handleChangePassword(",
                       "func (s *Server) handleLockoutStatus(",
                       "StatusTooManyRequests"):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)

    def test_cli_shapes(self):
        src = read("cmd/homesteadctl/admin.go")
        for symbol in ('"passwd"', '"lockout"',
                       "--current-password-file",
                       "--new-password-file",
                       "ChangePassword(", "LockoutStatus("):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)

    def test_parity_rows(self):
        matrix = read("docs/PARITY.md")
        for symbol in ("| /api/v1/admins/password |",
                       "| /api/v1/admins/lockout |"):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, matrix)


if __name__ == "__main__":
    unittest.main()
