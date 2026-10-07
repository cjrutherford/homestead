"""S123 — Public Exposure Wizard conformance tests.

Guardrails for guided widening: an ordered four-step flow over the
generic wizard store, cumulative validation with the planner proving
reviewability before anything persists, already-placed targets and
unplannable selections refused, reviewed plans recomputed from saved
inputs so reviews never go stale, and no HTTP surface. Behavior is
covered by Go tests in internal/exposure.
"""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def read(rel: str) -> str:
    return (REPO / rel).read_text(encoding="utf-8")


class TestExposureWizardContract(unittest.TestCase):
    def test_wizard_shapes(self):
        src = read("internal/exposure/wizard.go")
        for symbol in ("type Wizard", "func NewWizard(",
                       "func (w *Wizard) Save(",
                       "func (w *Wizard) Plan(", '"exposure"',
                       "already at"):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)

    def test_no_surface(self):
        src = read("internal/exposure/wizard.go")
        for banned in ("net/http", "HandleFunc", "Handle("):
            with self.subTest(banned=banned):
                self.assertNotIn(banned, src)


if __name__ == "__main__":
    unittest.main()
