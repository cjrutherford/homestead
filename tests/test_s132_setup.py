"""S132 — Assisted Router Setup conformance tests.

Guardrails for guided NAT setup: an ordered five-step flow over the
generic wizard store with gateway facts, validated purposes, and
renderable instructions required cumulatively plus owner
confirmation recording only the owner sees their router UI,
reviewable plans recomputed from saved inputs, UPnP mappings queried
read-only with 714 meaning absent and both WAN services attempted,
matches judged against the appliance address, and external proof
left to verification. Behavior is covered by Go tests in
internal/router against a scripted gateway.
"""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def read(rel: str) -> str:
    return (REPO / rel).read_text(encoding="utf-8")


class TestSetupContract(unittest.TestCase):
    def test_setup_shapes(self):
        src = read("internal/router/setup.go")
        for symbol in ("type Wizard", "func NewWizard(",
                       "func (w *Wizard) Save(",
                       "func (w *Wizard) Plan(",
                       "func QueryMappings(",
                       '"router-setup"', "GetSpecificPortMappingEntry"):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)

    def test_no_surface(self):
        src = read("internal/router/setup.go")
        for banned in ("HandleFunc", "Handle("):
            with self.subTest(banned=banned):
                self.assertNotIn(banned, src)


if __name__ == "__main__":
    unittest.main()
