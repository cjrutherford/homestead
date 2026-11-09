"""S466 - recovery flow only serves owner-issued one-time links."""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def read(rel):
    return (REPO / rel).read_text(encoding="utf-8")


class TestRecoveryPolicy(unittest.TestCase):
    def test_policy_in_recovery_flow(self):
        go = read("internal/sso/recovery.go")
        self.assertIn("is_restored", go)
        self.assertIn("pending_user", go)
        self.assertIn("ak_message", go)
        self.assertIn("ensureRecoveryPolicy", go)
        self.assertIn("policybindingmodel_ptr_id", go)

    def test_go_tests_pin_it(self):
        t = read("internal/sso/recovery_test.go")
        self.assertIn("TestRecoveryFlowRefusesVisitorsWithoutAOneTimeLink", t)
        self.assertIn("TestRecoveryPolicyIsAddedToAnExistingFlowAndIsIdempotent", t)

    def test_deferred_entry_removed(self):
        self.assertNotIn("Acceptance PLAT-PEOPLE-6 (medium)", read("docs/DEFERRED.md"))

    def test_adr_amended(self):
        self.assertIn("S466", read("docs/adr/ADR-049-people-administration-on-the-public-dashboard.md"))

    def test_slice_recorded(self):
        self.assertIn("S466", read("docs/SLICE_INDEX.md"))
        self.assertTrue(list((REPO / "docs" / "slices").glob("S466-*.md")))


if __name__ == "__main__":
    unittest.main()
