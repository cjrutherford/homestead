"""S131 — Gateway Discovery conformance tests.

Guardrails for NAT upstream identification: the likely gateway from
default-route facts with absent routes failing instead of guessing,
mapping support probed lightly (UPnP by multicast with WAN
connection control URLs, NAT-PMP/PCP by version on the shared port
with external addresses disclosed), unsupported protocols staying
out without error, and real handshakes left to later slices.
Behavior is covered by Go tests in internal/router against local
speakers.
"""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def read(rel: str) -> str:
    return (REPO / rel).read_text(encoding="utf-8")


class TestGatewayContract(unittest.TestCase):
    def test_discovery_shapes(self):
        src = read("internal/router/gateway.go")
        for symbol in ("func DiscoverGateway(", "func ProbePMP(",
                       "func DiscoverUPnP(", "type GatewayInfo",
                       "type UPnPInfo", '"upnp"', '"nat-pmp"',
                       '"pcp"'):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)


if __name__ == "__main__":
    unittest.main()
