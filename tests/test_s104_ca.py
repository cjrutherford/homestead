"""S104 — Internal CA conformance tests.

Guardrails for locally trusted HTTPS: a single-tier P-256 authority
with cert-signing usage and decade lifetime, storage round-trips
proving key-certificate match with mismatches and non-CA material
refused, server issuance under fresh keys with handshake usage and
server-auth EKU bounded to hours-through-years, names in the shared
canonical spelling with wildcards allowed, distinct serials per
issuance, and chains verifying against the CA pool. Trust-store
installation stays out (security baseline owns host mutation).
Behavior is covered by Go tests in internal/cert with real x509
verification.
"""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def read(rel: str) -> str:
    return (REPO / rel).read_text(encoding="utf-8")


class TestCAContract(unittest.TestCase):
    def test_ca_shapes(self):
        src = read("internal/cert/ca.go")
        for symbol in ("type Authority", "func GenerateCA(",
                       "func ParseAuthority(",
                       "func (a Authority) IssueServerCert(",
                       "MaxPathLenZero", "KeyUsageCertSign",
                       "ExtKeyUsageServerAuth"):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)

    def test_no_host_mutation(self):
        src = read("internal/cert/ca.go")
        for banned in ("os.WriteFile", "exec.Command", "update-ca"):
            with self.subTest(banned=banned):
                self.assertNotIn(banned, src)


if __name__ == "__main__":
    unittest.main()
