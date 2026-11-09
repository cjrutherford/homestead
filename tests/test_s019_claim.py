"""S019 — Secure Appliance Claim conformance tests.

Guardrails for the claim flow: tokens are hashed single-use with expiry,
the appliance moves UNCLAIMED to CLAIMED exactly once, identity and claim
are the only new routes, the CLI redeems without credentials (first admin
is S020), the daemon mints on fresh startup, and no session/password
machinery leaks in from S020/S021. Runtime behavior is covered by Go
tests in internal/claim, internal/server, internal/apiclient, and
cmd/homesteadctl.
"""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def read(rel: str) -> str:
    return (REPO / rel).read_text(encoding="utf-8")


class TestClaimService(unittest.TestCase):
    def test_token_lifecycle(self):
        src = read("internal/claim/claim.go")
        for op in ("func (s *Service) MintToken(", "func (s *Service) Claim(", "func (s *Service) CurrentState("):
            with self.subTest(op=op):
                self.assertIn(op, src)

    def test_hashes_not_plaintext(self):
        src = read("internal/claim/claim.go")
        self.assertIn("sha256", src)
        self.assertIn("token_hash", src)
        self.assertIn("ConstantTimeCompare", src)

    def test_single_use_and_expiry(self):
        src = read("internal/claim/claim.go")
        self.assertIn("used_at", src)
        self.assertIn("expires_at", src)
        self.assertIn("TokenTTL", src)

    def test_no_admin_or_session_logic(self):
        """S020 owns administrators and sessions; S021 owns recovery."""
        import re

        code = re.sub(r"(?m)^\s*//.*$", "", read("internal/claim/claim.go"))
        for banned in ("password", "session", "bcrypt", "recover"):
            with self.subTest(banned=banned):
                self.assertNotIn(banned, code.lower())

    def test_no_new_dependencies(self):
        """The claim package needs no crypto beyond stdlib (S020 owns bcrypt)."""
        src = read("internal/claim/claim.go")
        self.assertNotIn("golang.org/x/crypto", src)


class TestClaimSurface(unittest.TestCase):
    def test_migration_v2(self):
        src = read("internal/store/store.go")
        import re

        version = int(re.search(r"(?m)^const CurrentVersion = (\d+)$", src).group(1))
        self.assertGreaterEqual(version, 2)
        self.assertIn("CREATE TABLE IF NOT EXISTS appliance", src)
        self.assertIn("CREATE TABLE IF NOT EXISTS claim_tokens", src)

    def test_identity_and_claim_routes(self):
        src = read("internal/server/claim.go")
        self.assertIn("GET /api/v1/identity", src)
        self.assertIn("POST /api/v1/claim", src)
        self.assertIn("StatusUnauthorized", src)
        self.assertIn("StatusConflict", src)

    def test_client_covers_claim(self):
        src = read("internal/apiclient/claim.go")
        self.assertIn("func (c *Client) Identity(", src)
        self.assertIn("func (c *Client) Claim(", src)

    def test_cli_claim_without_credentials(self):
        src = (REPO / "cmd" / "homesteadctl" / "claim.go").read_text(encoding="utf-8")
        self.assertIn("runClaim", src)
        for banned in ("--password", "--username", "password-file"):
            with self.subTest(banned=banned):
                self.assertNotIn(banned, src)

    def test_daemon_mints_on_fresh_startup(self):
        main = read("cmd/homestead-server/main.go")
        self.assertIn("MountClaim", read("internal/server/mount.go"))
        self.assertIn("EnsureTokenFile", main)  # S288: file, not console
        self.assertIn('"data-dir"', main)


if __name__ == "__main__":
    unittest.main()
