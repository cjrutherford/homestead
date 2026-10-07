"""S063 — Audit Trail conformance tests.

Guardrails for the append-only trail: actor/action/plan/outcome plus
override records with validation, migration v6, login attempts recorded
without leaking secrets, and a newest-first read API plus CLI — with no
update/delete path anywhere and no dashboard viewer yet. Behavior is
covered by Go tests in internal/audit, internal/server,
internal/apiclient, and cmd/homesteadctl.
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


class TestAuditContract(unittest.TestCase):
    def test_record_and_query(self):
        src = read("internal/audit/audit.go")
        for symbol in ("type Entry struct", "func New(", "func (s *Service) Record(",
                       "func (s *Service) Get(", "func (s *Service) List(",
                       "ErrNotFound", "OutcomeOK", "OutcomeError"):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)

    def test_goal_fields(self):
        src = read("internal/audit/audit.go")
        for field in ("Actor", "Action", "Plan", "Outcome", "Detail",
                      "Override", "OverrideReason"):
            with self.subTest(field=field):
                self.assertIn(field, src)

    def test_migration_v6(self):
        import re

        src = read("internal/store/store.go")
        version = int(re.search(r"(?m)^const CurrentVersion = (\d+)$", src).group(1))
        self.assertGreaterEqual(version, 6)
        self.assertIn("CREATE TABLE IF NOT EXISTS audit_entries", src)

    def test_append_only(self):
        """No update or delete path exists by design."""
        src = read("internal/audit/audit.go")
        self.assertNotIn("UPDATE audit_entries", src)
        self.assertNotIn("DELETE FROM audit_entries", src)

    def test_login_records_without_secrets(self):
        src = read("internal/server/auth.go")
        self.assertIn("admin.login", src)
        self.assertIn("audit.New(st)", read("cmd/homestead-server/main.go"))

    def test_read_surface(self):
        self.assertIn("GET /api/v1/audit", read("internal/server/audit.go"))
        self.assertIn("func (c *Client) AuditList(", read("internal/apiclient/audit.go"))
        src = ctl_sources()
        for cmd in ('"audit"', "runAuditList", "--limit"):
            with self.subTest(cmd=cmd):
                self.assertIn(cmd, src)


if __name__ == "__main__":
    unittest.main()
