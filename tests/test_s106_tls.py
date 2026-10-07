"""S106 — TLS Wizard conformance tests.

Guardrails for guided TLS: an ordered four-step flow over the generic
wizard store, strategies validated as coherent sets (private names to
internal or manual issuance only, sharing the automation boundary),
prerequisites proven without fetching (dns-01 needs present
credentials plus a reachable directory, internal needs a configured
authority, manual and direct challenges need nothing checkable),
plans pairing strategy with readiness, and validation grading served
material through monitoring. Issuance runs between prerequisites and
validation at the caller's layer; the wizard guides and verifies. No
HTTP surface, so parity is untouched.
"""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def read(rel: str) -> str:
    return (REPO / rel).read_text(encoding="utf-8")


class TestTLSWizardContract(unittest.TestCase):
    def test_wizard_shapes(self):
        src = read("internal/cert/wizard.go")
        for symbol in ("type Wizard", "func NewWizard(",
                       "func (w *Wizard) Save(",
                       "func (w *Wizard) Prerequisites(",
                       "func (w *Wizard) Plan(",
                       "func (w *Wizard) ValidateIssued(",
                       "type Prereq", "type TLSPlan", '"tls"'):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)

    def test_guidance_promises(self):
        src = read("internal/cert/wizard.go")
        for symbol in ("needs internal or manual", "not configured",
                       "directory unreachable", "never fetches"):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)

    def test_no_surface(self):
        src = read("internal/cert/wizard.go")
        for banned in ("net/http", "HandleFunc", "Handle("):
            with self.subTest(banned=banned):
                self.assertNotIn(banned, src)


if __name__ == "__main__":
    unittest.main()
