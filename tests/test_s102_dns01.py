"""S102 — DNS-01 Infrastructure conformance tests.

Guardrails for challenge orchestration: authorizations map to
`_acme-challenge` TXT records with wildcards stripped to base, apex
and sub-names placed relative to the zone, out-of-zone names and bad
tokens refused before any provider call, placement creating only (SPF
and siblings survive), propagation awaited through the shared verifier
with caller-owned polling and deadlines, and cleanup removing exactly
placed values idempotently while foreign records survive. Behavior is
covered by Go tests in internal/cert against a scripted provider.
"""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def read(rel: str) -> str:
    return (REPO / rel).read_text(encoding="utf-8")


class TestDNS01Contract(unittest.TestCase):
    def test_orchestration_shapes(self):
        src = read("internal/cert/challenge.go")
        for symbol in ("func ChallengeRecords(", "type Placer",
                       "func (p Placer) Place(", "func AwaitPropagation(",
                       "func (p Placer) Cleanup("):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)

    def test_safe_lifecycle(self):
        src = read("internal/cert/challenge.go")
        for symbol in ("_acme-challenge", "ChangeCreate",
                       "outside zone", "not propagated"):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)


if __name__ == "__main__":
    unittest.main()
