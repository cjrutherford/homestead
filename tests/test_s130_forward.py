"""S130 — Manual Port Forward Workflow conformance tests.

Guardrails for NAT instructions: purposes mapping to exact port sets
with unknown purposes failing instead of guessing empty, merged sets
deduplicated and sorted, matched external/internal ports with
validated appliance addresses, duplicates and invalid forwards
refused before rendering, and instructions naming DHCP stability,
verification, CGNAT limits, and management restraint. Behavior is
covered by Go tests in internal/router.
"""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def read(rel: str) -> str:
    return (REPO / rel).read_text(encoding="utf-8")


class TestForwardContract(unittest.TestCase):
    def test_forward_shapes(self):
        src = read("internal/router/forward.go")
        for symbol in ("type Forward", "func RequiredForwards(",
                       "func RequiredSet(", "func RenderInstructions(",
                       '"web"', '"headscale"', "DHCP"):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)


if __name__ == "__main__":
    unittest.main()
