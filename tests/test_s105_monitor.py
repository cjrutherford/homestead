"""S105 — Certificate Monitoring conformance tests.

Guardrails for expiry tracking: chain inspection reporting names,
windows, issuer, depth, and days remaining; live handshake capture
with SNI that inspects without trusting; per-certificate grades
(healthy inside validity, expiring inside the renewal window, expired
past NotAfter, missing without material, mismatch on uncovered names,
invalid on unparseable material) reusing each certificate's own
renewal policy; and reporting only, never actuation. Behavior is
covered by Go tests in internal/cert with minted material and a live
TLS server.
"""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def read(rel: str) -> str:
    return (REPO / rel).read_text(encoding="utf-8")


class TestMonitorContract(unittest.TestCase):
    def test_monitor_shapes(self):
        src = read("internal/cert/monitor.go")
        for symbol in ("func InspectChain(", "func FetchChain(",
                       "type Status", "func Check(",
                       '"healthy"', '"expiring"', '"expired"',
                       '"missing"', '"mismatch"', '"invalid"'):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)

    def test_inspect_without_trusting(self):
        src = read("internal/cert/monitor.go")
        self.assertIn("InsecureSkipVerify", src)
        self.assertIn("inspection only", src)


if __name__ == "__main__":
    unittest.main()
