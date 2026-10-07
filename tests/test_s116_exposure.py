"""S116 — Route Exposure Policies conformance tests.

Guardrails for per-route policies: named policies over firewall
sources in one shared language, empty meaning deny-all with
zone-wide-only meaning firewall-governed, listed networks enforcing
at the proxy through allowlists with aborts elsewhere, TLS staying
site-level above enforcement, nil policies rendering byte-identical
output, and invalid policies refusing at validation. Behavior is
covered by Go tests in internal/docker with render goldens.
"""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def read(rel: str) -> str:
    return (REPO / rel).read_text(encoding="utf-8")


class TestExposureContract(unittest.TestCase):
    def test_policy_shapes(self):
        src = read("internal/docker/exposure.go")
        for symbol in ("type Policy", "func (p Policy) Validate()",
                       "remote_ip", "handle @allowed", "abort"):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)

    def test_route_attachment(self):
        src = read("internal/docker/route.go")
        self.assertIn("Policy", src)
        self.assertIn("r.Policy", src)


if __name__ == "__main__":
    unittest.main()
