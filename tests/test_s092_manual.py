"""S092 — Manual DNS Provider conformance tests.

Guardrails for human-operated public DNS: the provider carries no API
automation or credentials, publishes only the zone's public view as
exact master-file lines (LAN answers never leak, zones without a public
view fail instead of guessing), CNAME targets render absolute with TXT
quoted, propagation proves every desired value through public DNS with
per-type normalization while TTLs stay unobserved, gaps aggregate into
one report, and checks never depend on host resolver configuration.
Behavior is covered by Go tests in internal/dns.
"""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def read(rel: str) -> str:
    return (REPO / rel).read_text(encoding="utf-8")


class TestManualContract(unittest.TestCase):
    def test_manual_shapes(self):
        src = read("internal/dns/manual.go")
        for symbol in ("type Manual", "func (m Manual) PublicRecords(",
                       "func RenderZoneFile(", "type ProbeFunc",
                       "func ProbeVia(", "func VerifyRecords(",
                       '"manual"'):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)

    def test_exact_and_safe(self):
        src = read("internal/dns/manual.go")
        for symbol in ("$ORIGIN", "no public view", "Create these records exactly",
                       "not propagated", "ErrNoAPI"):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)

    def test_provider_conformance(self):
        inner = read("internal/dns/manual_test.go")
        self.assertIn("var _ Provider = Manual{}", inner)


if __name__ == "__main__":
    unittest.main()
