"""S115 — Overlay DNS conformance tests.

Guardrails for overlay resolution: split-DNS address records bridge
to control-plane extras with fully-qualified names while CNAME and
TXT stay out, specs validate base domains with usable nameservers and
agreeing extras, dns sections reconcile into deployed configurations
preserving every other key with invalid specs refusing before disk,
adoption plans write plus restart plus verify idempotently, and
Tailscale-side DNS stays admin-owned with no adapter pretending
otherwise. Behavior is covered by Go tests in internal/docker.
"""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def read(rel: str) -> str:
    return (REPO / rel).read_text(encoding="utf-8")


class TestOverlayDNSContract(unittest.TestCase):
    def test_dns_shapes(self):
        src = read("internal/docker/headscaledns.go")
        for symbol in ("func ExtraRecordsFor(", "type HeadscaleDNS",
                       "func WriteHeadscaleDNS(",
                       "func HeadscaleDNSDefinition(",
                       '"headscale.dns"', "extra_records",
                       "magic_dns", "base_domain"):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)


if __name__ == "__main__":
    unittest.main()
