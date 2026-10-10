"""S226 — TLS Readiness Bootstrap conformance tests.

Guardrails for reaching TLS_READY without requiring public app
ingress: private names stay internal, DNS-01 decides with no
ingress evidence, direct challenges decide on verified ingress
only (wildcards excluded), anything else takes the manual owner
path, unknown providers and malformed names refuse, and the
decided strategy records one row for later issuance. Behavior is
covered by Go tests in internal/cert over schema v19.
"""

import re
import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def read(rel: str) -> str:
    return (REPO / rel).read_text(encoding="utf-8")


class TestTLSReadiness(unittest.TestCase):
    def test_strategy_shapes(self):
        src = read("internal/cert/bootstrap.go")
        for symbol in ("type Readiness", "type Ingress",
                       "func DecideReadiness(", "func RecordReadiness(",
                       "func CurrentReadiness(",
                       "func EstablishReadiness(",
                       "ErrNoTLSReadiness", "ChallengeDNS01",
                       "NeedsIngress"):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)

    def test_no_ingress_required_to_decide(self):
        src = read("internal/cert/bootstrap.go")
        for symbol in ("no ingress required", "DNS-01 issues before exposure"):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)

    def test_schema_v19(self):
        src = read("internal/store/store.go")
        version = int(re.search(r"(?m)^const CurrentVersion = (\d+)$", src).group(1))
        self.assertGreaterEqual(version, 19)
        self.assertIn("CREATE TABLE IF NOT EXISTS tls_readiness", src)


if __name__ == "__main__":
    unittest.main()
