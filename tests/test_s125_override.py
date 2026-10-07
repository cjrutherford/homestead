"""S125 — Owner Overrides conformance tests.

Guardrails for explicit exceptions: per-host exposure overrides with
required bounded reasons and named actors including management
exposure flagged distinctly, absolute refusals for unreachable
targets and closed package ceilings, re-grants replacing with fresh
audit, idempotent revocation, reads enforcing with the trail
explaining, and every grant and revocation appended with override
marking. Behavior is covered by Go tests in internal/exposure
against temporary stores.
"""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def read(rel: str) -> str:
    return (REPO / rel).read_text(encoding="utf-8")


class TestOverrideContract(unittest.TestCase):
    def test_override_shapes(self):
        src = read("internal/exposure/overrides.go")
        for symbol in ("type Override", "func (o *Overrides) Grant(",
                       "func (o *Overrides) Revoke(",
                       "func (o *Overrides) Get(",
                       "func (o *Overrides) List(",
                       '"exposure.override"', '"exposure.revoke"',
                       "holds against overrides"):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)

    def test_persisted_audited(self):
        migration = read("internal/store/store.go")
        self.assertIn("exposure_overrides", migration)
        self.assertIn("CurrentVersion = 8", migration)


if __name__ == "__main__":
    unittest.main()
