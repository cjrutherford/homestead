"""S202 — Identity Provider Interface conformance tests.

Guardrails for the integration contract: providers declare known
protocol capabilities and validated credential requirements without
holding values, one live call proves the endpoint answers before
setup flows trust it, registration refuses duplicates and unknown
lookups, and local administrator identity stays out — this package
describes external providers without authenticating anyone.
Behavior is covered by Go tests in internal/identity.
"""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def read(rel: str) -> str:
    return (REPO / rel).read_text(encoding="utf-8")


class TestProviderContract(unittest.TestCase):
    def test_contract_shapes(self):
        src = read("internal/identity/provider.go")
        for symbol in ("type Capability", "type CredentialField",
                       "type Credentials", "type Provider interface",
                       "func ValidateProvider(", "type Registry",
                       "func (r *Registry) Register(",
                       "func (r *Registry) Get("):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)

    def test_contract_verbs(self):
        src = read("internal/identity/provider.go")
        for symbol in ("CheckConnection(", "RequiredCredentials(",
                       '"oidc"', '"saml"', '"ldap"',
                       "duplicate provider", "unknown provider"):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)

    def test_local_identity_stays_out(self):
        src = read("internal/identity/provider.go")
        self.assertIn("internal/auth", src)
        self.assertIn("never authenticates", src)


if __name__ == "__main__":
    unittest.main()
