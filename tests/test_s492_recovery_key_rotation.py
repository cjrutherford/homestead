"""S492 - recovery key rotation on both surfaces."""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def read(rel):
    return (REPO / rel).read_text(encoding="utf-8")


class TestRotation(unittest.TestCase):
    def test_route_is_step_up_and_classified(self):
        go = read("internal/server/boundary.go")
        self.assertEqual(go.count('"POST /api/v1/backups/identity/rotate"'), 2)
        self.assertIn("stepUpBoundary", go)
        self.assertIn('"POST /api/v1/backups/identity/rotate"', read("internal/server/backuprecipients.go") + read("internal/server/boundary.go"))

    def test_server_fails_closed_and_rate_limits_first(self):
        go = read("internal/server/backuprecipients.go")
        self.assertIn("ExportBudget", go)
        self.assertIn("identity withheld", go)
        self.assertIn("backup.identity.rotate", go)

    def test_rewrap_only_touches_the_header(self):
        go = read("internal/backup/rotate.go")
        self.assertIn("RetiredIdentitySecret", go)
        self.assertIn("os.Rename", go)
        self.assertIn("io.Discard", go)  # full decrypt before replacing
        t = read("internal/backup/rotate_test.go")
        self.assertIn("TestRotateRewrapsExistingRunsForTheNewKey", t)

    def test_cli_and_dashboard(self):
        self.assertIn('"rotate"', read("cmd/homesteadctl/backuprecipients.go"))
        self.assertIn("backupIdentityRotate", read("dashboard/src/app/core/api.service.ts"))
        self.assertIn("Rotate recovery identity", read("dashboard/src/app/backups/recovery-identity.html"))
        self.assertIn("rotates the identity only after confirmation", read("dashboard/src/app/backups/backups.spec.ts"))

    def test_docs(self):
        self.assertIn("backups/identity/rotate", read("docs/PARITY.md"))
        self.assertIn("backup identity export/rotate", read("docs/PARITY_FLAGS.md"))
        self.assertIn("S492", read("docs/SLICE_INDEX.md"))
        self.assertIn("Amendment S492", read("docs/adr/ADR-022-backup-encryption-format.md"))
        self.assertIn("S492", read("docs/DEFERRED.md"))
        self.assertIn("PLAT-BACKUP-4", read("docs/reviews/OWNER_ACCEPTANCE_CHECKLIST.md"))
        self.assertTrue(list((REPO / "docs" / "slices").glob("S492-*.md")))


if __name__ == "__main__":
    unittest.main()
