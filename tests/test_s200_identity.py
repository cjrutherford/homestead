"""S200 — Local Identity Hardening conformance tests.

Rotation proves the current credential and revokes every session,
recovery is a boundary that revokes sessions and clears lockout,
and logins throttle uniformly for known and unknown usernames so
lockout never becomes an account oracle. Behavior is covered by Go
tests in internal/auth over schema v12.
"""

import re
import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def read(rel: str) -> str:
    return (REPO / rel).read_text(encoding="utf-8")


class TestIdentityHardening(unittest.TestCase):
    def test_rotation_shapes(self):
        src = read("internal/auth/auth.go")
        for symbol in ("func (s *Service) ChangePassword(",
                       "revokeSessions",
                       "ErrWeakPassword"):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)

    def test_lockout_shapes(self):
        src = read("internal/auth/auth.go")
        for symbol in ("MaxLoginFailures",
                       "LoginAttemptWindow",
                       "LoginLockout",
                       "ErrAccountLocked",
                       "login_attempts"):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)

    def test_recovery_boundary(self):
        src = read("internal/auth/recover.go")
        for symbol in ("revokeSessions", "clearFailures"):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)

    def test_schema_v12(self):
        src = read("internal/store/store.go")
        version = int(re.search(r"(?m)^const CurrentVersion = (\d+)$", src).group(1))
        self.assertGreaterEqual(version, 12)
        self.assertIn("CREATE TABLE IF NOT EXISTS login_attempts", src)


if __name__ == "__main__":
    unittest.main()
