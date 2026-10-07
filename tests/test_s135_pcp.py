"""S135 — PCP Integration conformance tests.

Guardrails for safe PCP mapping: version-2 MAP datagrams with
nonces, lifetimes defaulting instead of fossilizing, deletions by
zero lifetime converging, gateway port reassignments failing loudly
for fixed-port serving, result codes and nonce mismatches
diagnosing, silence exhausting retries, no query operation claimed,
and disabled automation refusing before the network. Behavior is
covered by Go tests in internal/router against a scripted speaker.
"""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def read(rel: str) -> str:
    return (REPO / rel).read_text(encoding="utf-8")


class TestPCPContract(unittest.TestCase):
    def test_pcp_shapes(self):
        src = read("internal/router/pcp.go")
        for symbol in ("func NewPCP(", '"pcp"',
                       "func (p *PCP) AddMapping(",
                       "func (p *PCP) DeleteMapping(",
                       "func (p *PCP) QueryMapping(",
                       "ErrUnsupported", "ErrNotEnabled",
                       "assigned external port"):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)


if __name__ == "__main__":
    unittest.main()
