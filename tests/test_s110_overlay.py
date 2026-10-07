"""S110 — Remote Access Domain conformance tests.

Guardrails for provider-neutral overlay intent: none/headscale/
tailscale providers with disabled meaning unconfigured, staged subnet
routes refusing default and unroutable destinations, overlay DNS
intent with canonical search and usable nameservers, observed nodes
and networks validated for shape with lonely-but-working tailnets
legal, and adapters owning all provider contact. Behavior is covered
by Go tests in internal/overlay.
"""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def read(rel: str) -> str:
    return (REPO / rel).read_text(encoding="utf-8")


class TestOverlayContract(unittest.TestCase):
    def test_overlay_shapes(self):
        src = read("internal/overlay/model.go")
        for symbol in ("type Provider", "type SubnetRoute",
                       "type DNSPreferences", "type Config",
                       "type Node", "type Network",
                       "func (c Config) Validate()",
                       "func (n Network) Validate()"):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)

    def test_neutral_vocabulary(self):
        src = read("internal/overlay/model.go")
        for symbol in ('"none"', '"headscale"', '"tailscale"',
                       "takes no settings", "exit-node",
                       "duplicate route", "duplicate node"):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)

    def test_no_provider_contact(self):
        src = read("internal/overlay/model.go")
        for banned in ("net/http", "HandleFunc", "exec.Command"):
            with self.subTest(banned=banned):
                self.assertNotIn(banned, src)


if __name__ == "__main__":
    unittest.main()
