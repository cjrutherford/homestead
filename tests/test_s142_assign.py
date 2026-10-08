"""S142 — Storage Assignment conformance tests.

Guardrails for class-to-mount mapping: suitability ranked against
live facts (accessible, writable, never pseudo, class-locality for
databases, minimum free space), assignment requiring approval plus
current suitability with re-assignment migrating, shared mounts
across classes, idempotent removal, reads failing on the unbound,
and bindings persisted over schema v10. Behavior is covered by Go
tests in internal/storage against fixture discovery.
"""

import re
import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def read(rel: str) -> str:
    return (REPO / rel).read_text(encoding="utf-8")


class TestAssignContract(unittest.TestCase):
    def test_assign_shapes(self):
        src = read("internal/storage/assign.go")
        for symbol in ("func SuitableMounts(", "type Assignment",
                       "func (a *Assignments) Assign(",
                       "func (a *Assignments) Unassign(",
                       "func (a *Assignments) Get(",
                       "func (a *Assignments) List(",
                       "ErrNoAssignment"):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)

    def test_persisted(self):
        migration = read("internal/store/store.go")
        self.assertIn("storage_assignments", migration)
        version = int(re.search(r"(?m)^const CurrentVersion = (\d+)$", migration).group(1))
        self.assertGreaterEqual(version, 10)


if __name__ == "__main__":
    unittest.main()
