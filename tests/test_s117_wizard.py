"""S117 — Remote Access Wizard conformance tests.

Guardrails for guided remote access: an ordered five-step flow over
the generic wizard store, cumulative validation with settings
disagreements refused and Headscale server settings required,
credential values refused in state with provider-exact storage rules
(Tailscale adopting keyless, Headscale requiring pre-auth),
prerequisites proving binary presence plus settings plus stored
secrets, connectivity verified as online-with-addresses with
local-only trivial, and plans carrying config with server and user
for enrollers. Enrolment runs between credentials and validation at
the caller's layer. No HTTP surface, so parity is untouched.
"""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def read(rel: str) -> str:
    return (REPO / rel).read_text(encoding="utf-8")


class TestRemoteWizardContract(unittest.TestCase):
    def test_wizard_shapes(self):
        src = read("internal/overlay/wizard.go")
        for symbol in ("type RemoteWizard", "func NewRemoteWizard(",
                       "func (w *RemoteWizard) Save(",
                       "func (w *RemoteWizard) StoreCredentials(",
                       "func (w *RemoteWizard) Prerequisites(",
                       "func (w *RemoteWizard) ValidateConnectivity(",
                       "func (w *RemoteWizard) Plan(",
                       "type RemotePlan", '"remote"'):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)

    def test_guidance_promises(self):
        src = read("internal/overlay/wizard.go")
        for symbol in ("secret store, not wizard state",
                       "adopts existing login",
                       "node not online", "never enrols"):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)

    def test_no_surface(self):
        src = read("internal/overlay/wizard.go")
        for banned in ("net/http", "HandleFunc", "Handle("):
            with self.subTest(banned=banned):
                self.assertNotIn(banned, src)


if __name__ == "__main__":
    unittest.main()
