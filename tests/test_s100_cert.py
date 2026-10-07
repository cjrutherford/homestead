"""S100 — Certificate Domain Model conformance tests.

Guardrails for TLS identities: canonical DNS names with one wildcard
label allowed, issuers per type (internal bare, ACME with https
directory and known challenge, dns-01 bound to a provider, manual
bare), lifecycle states with failure text only on failed, renewal
policies with manual auto-renew refused and window math for monitors,
tables with unique names and single-owner DNS names plus lookup. The
model validates shape only — issuance mechanics belong to later
slices. Behavior is covered by Go tests in internal/cert.
"""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def read(rel: str) -> str:
    return (REPO / rel).read_text(encoding="utf-8")


class TestCertContract(unittest.TestCase):
    def test_cert_shapes(self):
        src = read("internal/cert/model.go")
        for symbol in ("type IssuerType", "type ChallengeType",
                       "type Issuer", "type State", "type Renewal",
                       "type Certificate", "type Set",
                       "func (c Certificate) Validate()",
                       "func (s Set) Validate()",
                       "func (r Renewal) RenewalDue("):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)

    def test_issuer_vocabulary(self):
        src = read("internal/cert/model.go")
        for symbol in ('"internal"', '"acme"', '"manual"', '"http-01"',
                       '"tls-alpn-01"', '"dns-01"', '"pending"',
                       '"issued"', '"failed"', "claimed by both",
                       "cannot auto-renew"):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)


if __name__ == "__main__":
    unittest.main()
