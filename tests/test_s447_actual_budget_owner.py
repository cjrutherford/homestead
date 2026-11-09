"""S447: the appliance owner is Actual Budget's server owner at install."""
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent


def read(path):
    return (ROOT / path).read_text(encoding="utf-8")


class S447ActualBudgetOwnerTest(unittest.TestCase):
    def test_manifest_declares_owner_first_run(self):
        y = read("catalog/actual-budget.yaml")
        assert "routine: actual-budget" in y
        assert "Sign in yourself first" not in y


    def test_routine_seeds_users_table_and_is_pinned(self):
        g = read("internal/firstrun/actualbudget.go")
        assert "INSERT INTO users" in g and "ActualBudgetVerified" in g


    def test_checklist_has_no_race(self):
        t = read("docs/reviews/OWNER_ACCEPTANCE_CHECKLIST.md")
        assert "before anyone else does" not in t.split("### SRV-ACTUAL-BUDGET:")[1].split("\n### ")[0]


    def test_every_package_has_curated_copy(self):
        c = read("dashboard/src/app/catalog/curated-copy.ts")
        assert "'actual-budget'" in c and "'vikunja'" in c


if __name__ == "__main__":
    unittest.main()
