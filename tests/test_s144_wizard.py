"""S144 — Storage Wizard conformance tests.

Guardrails for guided storage setup: an ordered four-step flow over
the generic wizard store with shape-checked mounts and validated
class bindings, readiness previewed against live facts with reasons
where not, approvals and bindings applied at the caller's layer with
offenders named, unregistered mounts refusing assignment, and no HTTP
surface. Behavior is covered by Go tests in internal/storage
against fixture discovery and temporary stores.
"""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def read(rel: str) -> str:
    return (REPO / rel).read_text(encoding="utf-8")


class TestStorageWizardContract(unittest.TestCase):
    def test_wizard_shapes(self):
        src = read("internal/storage/wizard.go")
        for symbol in ("type Wizard", "func NewWizard(",
                       "func (w *Wizard) Save(",
                       "func (w *Wizard) Plan(",
                       "func (w *Wizard) ApplyMounts(",
                       "func (w *Wizard) ApplyAssignments(",
                       "type SetupPlan", '"storage"'):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)

    def test_no_surface(self):
        src = read("internal/storage/wizard.go")
        for banned in ("net/http", "HandleFunc", "Handle("):
            with self.subTest(banned=banned):
                self.assertNotIn(banned, src)


if __name__ == "__main__":
    unittest.main()
