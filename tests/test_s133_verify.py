"""S133 — Port Forward Verification conformance tests.

Guardrails for ingress attribution: kernel socket tables deciding
service evidence with dials deciding firewall evidence, ordered
layers naming the first failure without blending faults, UPnP
mappings proving router rules with mismatches carrying evidence,
unaskable gateways reporting unknown with outside proof named next,
invalid checks failing before probing, and hairpin-prone external
claims left out. Behavior is covered by Go tests in internal/router
with fixture tables and live sockets.
"""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def read(rel: str) -> str:
    return (REPO / rel).read_text(encoding="utf-8")


class TestVerifyContract(unittest.TestCase):
    def test_verify_shapes(self):
        src = read("internal/router/verify.go")
        for symbol in ("func VerifyForward(", "type Verdict",
                       "type LayerVerdict", '"service"',
                       '"host-firewall"', '"router"',
                       "Responsible"):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)


if __name__ == "__main__":
    unittest.main()
