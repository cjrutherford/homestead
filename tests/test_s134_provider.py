"""S134 — Router Provider Interface conformance tests.

Guardrails for automated mapping: add, remove, and read behind an
explicit opt-in with disabled automation refusing consent-first,
incapable providers failing distinct from disabled ones, absent
entries distinct from both, bounded lifetimes refreshing instead of
fossilizing, validated metadata with duplicate registration
refused, and a manual provider carrying no automation. Behavior is
covered by Go tests in internal/router.
"""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def read(rel: str) -> str:
    return (REPO / rel).read_text(encoding="utf-8")


class TestRouterProviderContract(unittest.TestCase):
    def test_contract_shapes(self):
        src = read("internal/router/provider.go")
        for symbol in ("type Provider interface", "type Mapping",
                       "func (m Mapping) Validate(",
                       "func (m Mapping) Key(",
                       "func ValidateProvider(", "type Registry",
                       "ErrUnsupported", "ErrNotEnabled", "ErrNoMapping",
                       '"mapping"', '"query"'):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)


if __name__ == "__main__":
    unittest.main()
