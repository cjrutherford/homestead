"""S020 — Administrator Authentication conformance tests.

Guardrails for first-admin auth: bcrypt-hashed credentials and hashed
session tokens in the store, setup exactly once after claim, login/logout
endpoints with the right statuses, client coverage, CLI parity without a
recover command (S021 owns recovery), and no endpoint enforcement yet
(sessions are created, not yet required). Runtime behavior is covered by
Go tests in internal/auth, internal/server, internal/apiclient, and
cmd/homesteadctl.
"""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent
CTL = REPO / "cmd" / "homesteadctl"


def read(rel: str) -> str:
    return (REPO / rel).read_text(encoding="utf-8")


def ctl_sources() -> str:
    return "\n".join(
        p.read_text(encoding="utf-8") for p in CTL.glob("*.go") if not p.name.endswith("_test.go")
    )


class TestAuthService(unittest.TestCase):
    def test_lifecycle_operations(self):
        src = read("internal/auth/auth.go")
        for op in (
            "func (s *Service) SetupAdmin(",
            "func (s *Service) Login(",
            "func (s *Service) Verify(",
            "func (s *Service) Logout(",
        ):
            with self.subTest(op=op):
                self.assertIn(op, src)

    def test_bcrypt_and_hashes(self):
        src = read("internal/auth/auth.go")
        self.assertIn("golang.org/x/crypto/bcrypt", src)
        self.assertIn("GenerateFromPassword", src)
        self.assertIn("CompareHashAndPassword", src)
        self.assertIn("token_hash", src)

    def test_password_policy_and_session_ttl(self):
        src = read("internal/auth/auth.go")
        self.assertIn("MinPasswordLength", src)
        self.assertIn("IdleTTL", src)
        self.assertIn("AbsoluteTTL", src)

    def test_setup_gated_on_claim(self):
        src = read("internal/auth/auth.go")
        self.assertIn("ErrNotClaimed", src)
        self.assertIn("ErrAdminExists", src)

    def test_no_recovery_logic(self):
        """S021 owns root-only recovery; no backdoors here."""
        import re

        code = re.sub(r"(?m)^\s*//.*$", "", read("internal/auth/auth.go"))
        self.assertNotIn("recover", code.lower())


class TestAuthSurface(unittest.TestCase):
    def test_migration_v3(self):
        import re

        src = read("internal/store/store.go")
        version = int(re.search(r"(?m)^const CurrentVersion = (\d+)$", src).group(1))
        self.assertGreaterEqual(version, 3)
        self.assertIn("password_hash", src)
        self.assertIn("identities_username_unique", src)
        self.assertIn("ALTER TABLE sessions ADD COLUMN token_hash", src)

    def test_auth_endpoints(self):
        src = read("internal/server/auth.go")
        self.assertIn("POST /api/v1/admins/setup", src)
        self.assertIn("POST /api/v1/sessions", src)
        self.assertIn("DELETE /api/v1/sessions", src)
        self.assertIn("StatusCreated", src)
        self.assertIn("StatusNoContent", src)
        self.assertIn("Bearer ", src)

    def test_client_covers_auth(self):
        src = read("internal/apiclient/auth.go")
        self.assertIn("func (c *Client) SetupAdmin(", src)
        self.assertIn("func (c *Client) Login(", src)
        self.assertIn("func (c *Client) Logout(", src)

    def test_cli_covers_auth(self):
        import re

        code = re.sub(r"(?m)^\s*//.*$", "", ctl_sources())
        for cmd in ('"admin"', '"login"', '"logout"', "--password-file"):
            with self.subTest(cmd=cmd):
                self.assertIn(cmd, code)

    def test_daemon_mounts_auth(self):
        main = read("cmd/homestead-server/main.go")
        self.assertIn("MountAuth", read("internal/server/mount.go"))


if __name__ == "__main__":
    unittest.main()
