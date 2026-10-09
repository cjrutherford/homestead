"""S204 — SSO Setup Wizard conformance tests.

Guardrails for guided per-app SSO: the wizard walks package,
provider, review, and done over the generic wizard store, refusing
unsupported apps, unregistered providers, and protocols either
side does not speak; plans review without writing and carry the
recovery boundary in the record — Homestead administrator login
and socket recovery stay local, only app logins change; apply
records one intent per application under the acting owner. The
wizard never touches administrator identities: recovery
independence holds by construction. Behavior is covered by Go
tests in internal/catalog over schema v13.
"""

import re
import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def read(rel: str) -> str:
    return (REPO / rel).read_text(encoding="utf-8")


class TestSSOWizard(unittest.TestCase):
    def test_wizard_shapes(self):
        src = read("internal/catalog/ssowizard.go")
        for symbol in ("SSOWizardID", "SSOSteps",
                       "type Integration", "type Wizard",
                       "func (w *Wizard) Save(",
                       "func (w *Wizard) Plan(",
                       "func (w *Wizard) Apply(",
                       "LocalFallback", "RecoveryNote"):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)

    def test_recovery_independent_by_construction(self):
        src = read("internal/catalog/ssowizard.go")
        self.assertNotIn("internal/auth", src)
        self.assertNotIn("FROM sessions", src)
        self.assertNotIn("FROM identities", src)
        self.assertNotIn("UPDATE identities", src)
        self.assertIn("stay local", src)

    def test_schema_v13(self):
        src = read("internal/store/store.go")
        version = int(re.search(r"(?m)^const CurrentVersion = (\d+)$", src).group(1))
        self.assertGreaterEqual(version, 13)
        self.assertIn("CREATE TABLE IF NOT EXISTS sso_integrations", src)


if __name__ == "__main__":
    unittest.main()
