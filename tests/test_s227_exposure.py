"""S227 — Public Exposure Bootstrap conformance tests.

Guardrails for approval-gated application: approvals name the
actor, re-validate the edge with fresh alignment, and refuse
stale, misaligned, and no-op plans; records replace per host for
the audit trail; application composes the management baseline
with every approval (never dropping earlier routes), substitutes
owner-approved CIDRs for zone-wide wide sources, collapses exact
duplicates, refuses same-named conflicts naming both owners, and
writes the edge snippet with a proxy reload — unapproved hosts
refuse before anything runs. Behavior is covered by Go tests in
internal/exposure over schema v20.
"""

import re
import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def read(rel: str) -> str:
    return (REPO / rel).read_text(encoding="utf-8")


class TestExposureApply(unittest.TestCase):
    def test_approval_shapes(self):
        src = read("internal/exposure/apply.go")
        for symbol in ("type Approval", "func Approve(",
                       "func RecordApproval(", "func ApprovalFor(",
                       "func Approvals(", "ErrNoApproval",
                       "no longer aligned", "already serves"):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)

    def test_apply_shapes(self):
        src = read("internal/exposure/apply.go")
        for symbol in ("func ApplyDefinition(", '"exposure.apply"',
                       "func refinePolicies(", "needs explicit CIDRs",
                       "conflicting firewall rule",
                       "route not approved"):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)

    def test_schema_v20(self):
        src = read("internal/store/store.go")
        version = int(re.search(r"(?m)^const CurrentVersion = (\d+)$", src).group(1))
        self.assertGreaterEqual(version, 20)
        self.assertIn("CREATE TABLE IF NOT EXISTS exposure_approvals", src)


if __name__ == "__main__":
    unittest.main()
