"""S137 — UPnP IGD Integration conformance tests.

Guardrails for opt-in UPnP mapping: control URLs validating as
http/https with hosts, mapping plus single-entry query reusing the
read path, explicit leases defaulting instead of fossilizing,
conflicts failing loudly with codes attached, absent deletes
converging with absent reads reporting, invalid mappings refused
before the network, disabled automation refusing first, and review
living one layer up in the assisted flow. Behavior is covered by Go
tests in internal/router against a stateful SOAP double.
"""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def read(rel: str) -> str:
    return (REPO / rel).read_text(encoding="utf-8")


class TestUPnPContract(unittest.TestCase):
    def test_upnp_shapes(self):
        src = read("internal/router/upnp.go")
        for symbol in ("func NewUPnP(", '"upnp"',
                       "func (u *UPnP) AddMapping(",
                       "func (u *UPnP) DeleteMapping(",
                       "func (u *UPnP) QueryMapping(",
                       "AddPortMapping", "DeletePortMapping",
                       "ErrNotEnabled", "ErrNoMapping"):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)


if __name__ == "__main__":
    unittest.main()
