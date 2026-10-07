"""S126 — External Connectivity Validation conformance tests.

Guardrails for public proof: DNS resolving to the expected address,
both edge ports accepting TCP, TLS verifying against system roots
with trust failures classified (unknown authority, invalid, name
mismatch), served responses meeting path/status/body contracts, no
short-circuiting so one run reports every layer, dial overrides for
split-horizon and test reachability, and plaintext confined to LAN
and test use. Behavior is covered by Go tests in internal/exposure
against live local servers.
"""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def read(rel: str) -> str:
    return (REPO / rel).read_text(encoding="utf-8")


class TestValidateContract(unittest.TestCase):
    def test_validate_shapes(self):
        src = read("internal/exposure/validation.go")
        for symbol in ("type Target", "type Finding", "type Report",
                       "func Validate(", '"dns"', '"tcp-80"',
                       '"tcp-443"', '"tls"', '"http"',
                       "not publicly trusted"):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)


if __name__ == "__main__":
    unittest.main()
