"""S091 — Public DNS Provider Interface conformance tests.

Guardrails for the reconciliation contract: providers declare known
capabilities and validated credential requirements without holding
values, desired-versus-observed diffing is pure set math with stable
create-before-delete ordering (drift surfacing as delete/create pairs),
the provider surface is list plus ordered apply, providers without an
API fail those methods explicitly, registration refuses duplicates and
unknown lookups, and destructive application stays gated at the
reconciliation layer — never widened by providers. Behavior is covered
by Go tests in internal/dns.
"""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def read(rel: str) -> str:
    return (REPO / rel).read_text(encoding="utf-8")


class TestProviderContract(unittest.TestCase):
    def test_contract_shapes(self):
        src = read("internal/dns/provider.go")
        for symbol in ("type Capability", "type CredentialField",
                       "type Credentials", "type ChangeAction",
                       "type Change", "func DiffRecords(",
                       "type Provider interface", "func ValidateProvider(",
                       "type Registry", "func (r *Registry) Register(",
                       "func (r *Registry) Get(", "ErrNoAPI"):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)

    def test_contract_verbs(self):
        src = read("internal/dns/provider.go")
        for symbol in ("ListRecords(", "ApplyChanges(", "RequiredCredentials(",
                       '"create"', '"delete"', '"records"', '"acme-dns01"',
                       "duplicate provider", "unknown provider"):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)

    def test_diff_is_math_not_policy(self):
        src = read("internal/dns/provider.go")
        self.assertIn("S098", src)
        self.assertIn("never widen", src)


if __name__ == "__main__":
    unittest.main()
