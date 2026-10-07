"""S099 — DNS Diagnostics conformance tests.

Guardrails for failure explanations: per-record findings in the doctor
vocabulary (ok serves the value, fail serves nothing or the wrong
thing, warn marks timeouts and probe trouble), details naming causes
(NXDOMAIN, empty types, mismatches with what answered, unreachable
resolvers), TTLs kept out as unobservable, roll-up fail over warn over
ok, and view verification diagnosing merged audience tables rather
than bare baselines. Behavior is covered by Go tests in internal/dns.
"""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def read(rel: str) -> str:
    return (REPO / rel).read_text(encoding="utf-8")


class TestDiagnosticsContract(unittest.TestCase):
    def test_diagnostics_shapes(self):
        src = read("internal/dns/diagnostics.go")
        for symbol in ("type Finding", "type Report",
                       "func (r Report) Overall()",
                       "func Diagnose(", "func VerifyView(",
                       "FindingOK", "FindingWarn", "FindingFail"):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)

    def test_explanations(self):
        src = read("internal/dns/diagnostics.go")
        for symbol in ("NXDOMAIN", "stale cache", "wrong view",
                       "timed out", "no %s records"):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)


if __name__ == "__main__":
    unittest.main()
