"""S479 - public-name move safety."""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def read(rel):
    return (REPO / rel).read_text(encoding="utf-8")


class MoveSafety(unittest.TestCase):
    def test_probe_runs_after_verify_and_before_removal(self):
        code = read("internal/server/appmove.go")
        self.assertLess(code.index('Name: "verify-path"'), code.index("probeStep(plan)"))
        self.assertLess(code.index("probeStep(plan)"), code.index('Name: "connector"'))
        self.assertIn('"pending-clear"', code)

    def test_finish_flow_and_state(self):
        code = read("internal/server/appmove_finish.go")
        self.assertIn("buildFinishPlan", code)
        self.assertIn("app_move_pending", read("internal/store/store.go"))
        self.assertIn("finish", read("internal/apiclient/apppublic.go"))
        self.assertIn("Finish the move", read("dashboard/src/app/catalog/app-move.ts"))

    def test_doctor_check(self):
        self.assertIn('"public-name-plan"', read("internal/server/doctorlive.go"))
        self.assertIn("public-name-plan", read("docs/PARITY.md"))

    def test_audit_waiters_are_awaited_not_slept(self):
        self.assertIn("func (s *Server) waitAudits()", read("internal/server/catalog.go"))
        for rel in ("internal/server/apppublic_test.go", "internal/server/readlinks_test.go"):
            self.assertIn("waitAudits()", read(rel))

    def test_docs(self):
        self.assertIn("S479", read("docs/SLICE_INDEX.md"))
        self.assertIn("S479", read("docs/SLICE_PLAN.md"))
        self.assertIn("S479", read("docs/DEFERRED.md"))
        self.assertTrue((REPO / "docs/slices/S479-public-name-move-safety.md").exists())


if __name__ == "__main__":
    unittest.main()
