"""S165 — Jellyfin TLS conformance tests.

Guardrails for media TLS: readiness gating per strategy without
fetching (authority presence, challenge validity, plaintext
acknowledged), internal minting under fresh keys with pre-store
verification, material proofs covering exact and single-label
wildcard names inside validity windows, and route spelling kept
distinct from certificate spelling. Behavior is covered by Go tests
in internal/jellyfin with minted authorities.
"""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def read(rel: str) -> str:
    return (REPO / rel).read_text(encoding="utf-8")


class TestJellyfinTLSContract(unittest.TestCase):
    def test_tls_shapes(self):
        src = read("internal/jellyfin/tls.go")
        for symbol in ("func CheckReadiness(", "func IssueInternal(",
                       "func VerifyCertificate(",
                       "func WildcardMatch("):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)


if __name__ == "__main__":
    unittest.main()
