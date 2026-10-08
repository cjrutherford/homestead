"""S184 — Backup Wizard conformance tests.

Guardrails for guided scheduling: an ordered four-step flow over the
generic wizard store with package plus validated cadence, plans
previewing without writing, schedules persisting under acting
owners, and no HTTP surface. Behavior is covered by Go tests in
internal/backup against temporary stores.
"""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def read(rel: str) -> str:
    return (REPO / rel).read_text(encoding="utf-8")


class TestBackupWizardContract(unittest.TestCase):
    def test_wizard_shapes(self):
        src = read("internal/backup/wizard.go")
        for symbol in ("type Wizard", "func NewWizard(",
                       "func (w *Wizard) Save(",
                       "func (w *Wizard) Plan(",
                       "func (w *Wizard) ApplySchedule(",
                       '"backup"'):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)

    def test_no_surface(self):
        src = read("internal/backup/wizard.go")
        for banned in ("net/http", "HandleFunc", "Handle("):
            with self.subTest(banned=banned):
                self.assertNotIn(banned, src)


if __name__ == "__main__":
    unittest.main()
