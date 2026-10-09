"""S222 — Local Security Baseline conformance tests.

Guardrails for the pre-exposure gate: one readiness report over
claim, administrator, firewall, and local management TLS, ready
only when every leg passes with remedies in details; firewall
demands applied Homestead rules with unreadable rulesets failing
as unverifiable; management TLS proves issue-and-verify in pure
crypto with margin; unwired dependencies read as incomplete,
never ready. Behavior is covered by Go tests in
internal/baseline.
"""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def read(rel: str) -> str:
    return (REPO / rel).read_text(encoding="utf-8")


class TestSecurityBaseline(unittest.TestCase):
    def test_assessment_shapes(self):
        src = read("internal/baseline/baseline.go")
        for symbol in ("type Check", "type Report",
                       "func Assess(", "StatusPass", "StatusFail",
                       "HasHomesteadTable", "IssueServerCert",
                       "homestead.local"):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)

    def test_admin_accessor(self):
        src = read("internal/auth/auth.go")
        self.assertIn("func (s *Service) HasAdmin() (bool, error)", src)

    def test_gate_semantics(self):
        src = read("internal/baseline/baseline.go")
        for symbol in ("Ready only when every check",
                       "unreadable", "not applied",
                       "unclaimed", "no administrator"):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)


if __name__ == "__main__":
    unittest.main()
