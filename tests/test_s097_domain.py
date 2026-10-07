"""S097 — Domain Setup Wizard conformance tests.

Guardrails for guided domain setup: an ordered six-step flow over the
generic wizard store, cumulative payload validation (registered
provider, relative lowercase zone, lowercase base), credential values
refused in wizard state and stored under conventional secret names with
exact field sets, validation proving auth and zone access through the
live provider (manual passing on syntax with propagation deferred),
and a plan emitting provider, zone, base, and FQDN without mutating
provider state. Behavior is covered by Go tests in internal/domain;
the service owns no HTTP surface, so parity is untouched.
"""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def read(rel: str) -> str:
    return (REPO / rel).read_text(encoding="utf-8")


class TestDomainContract(unittest.TestCase):
    def test_wizard_shapes(self):
        src = read("internal/domain/domain.go")
        for symbol in ("type Service", "func New(", "func Next(",
                       "func (s *Service) Save(",
                       "func (s *Service) StoreCredentials(",
                       "func (s *Service) Validate(",
                       "func (s *Service) Plan(", "type Plan",
                       '"domain"'):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)

    def test_secrets_stay_out(self):
        src = read("internal/domain/domain.go")
        for symbol in ("belongs in the secret store", "dns_",
                       "missing credential"):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)

    def test_no_surface(self):
        src = read("internal/domain/domain.go")
        for banned in ("net/http", "HandleFunc", "Handle("):
            with self.subTest(banned=banned):
                self.assertNotIn(banned, src)


if __name__ == "__main__":
    unittest.main()
