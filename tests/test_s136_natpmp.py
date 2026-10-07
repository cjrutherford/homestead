"""S136 — NAT-PMP Integration conformance tests.

Guardrails for safe NAT-PMP mapping: version-0 family datagrams
with lifetimes defaulting instead of fossilizing, deletions by zero
lifetime converging, gateway port reassignments failing loudly for
fixed-port serving, result codes diagnosing, silence exhausting
retries, no query operation claimed, and disabled automation
refusing before the network. Behavior is covered by Go tests in
internal/router against a scripted speaker.
"""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def read(rel: str) -> str:
    return (REPO / rel).read_text(encoding="utf-8")


class TestNATPMPContract(unittest.TestCase):
    def test_natpmp_shapes(self):
        src = read("internal/router/natpmp.go")
        for symbol in ("func NewNATPMP(", '"nat-pmp"',
                       "func (p *NATPMP) AddMapping(",
                       "func (p *NATPMP) DeleteMapping(",
                       "func (p *NATPMP) QueryMapping(",
                       "ErrUnsupported", "ErrNotEnabled",
                       "assigned external port"):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)


if __name__ == "__main__":
    unittest.main()
