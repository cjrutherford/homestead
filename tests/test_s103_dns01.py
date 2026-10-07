"""S103 — Provider-backed DNS-01 conformance tests.

Guardrails for wired issuance: the three API providers register
against production endpoints with manual left out, issuers resolve to
providers only for dns-01 with credentials assembled from secrets
under conventional names, a minimal RFC 8555 client (dns-01 only,
direct challenges stay Caddy-native) signs with P-256 JWS plus nonce
discipline and badNonce retry, terms refuse without owner agreement,
and issuance places challenges, awaits propagation, triggers,
finalizes with a fresh CSR key, downloads the chain, and cleans up
even on failure. Behavior is covered by Go tests in internal/cert
against a signature-verifying scripted CA.
"""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def read(rel: str) -> str:
    return (REPO / rel).read_text(encoding="utf-8")


class TestProviderDNS01Contract(unittest.TestCase):
    def test_wiring_shapes(self):
        src = read("internal/cert/dns01.go")
        for symbol in ("func DefaultProviders(", "type DNS01",
                       "func (d *DNS01) Issue(", "SecretName",
                       "not dns-01"):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)

    def test_acme_shapes(self):
        src = read("internal/cert/acme.go")
        for symbol in ("ES256", "Replay-Nonce", "badNonce",
                       "newAccount", "newOrder", "acmeDirectory",
                       "keyAuthorization", "finalizeCSR",
                       "terms of service require owner agreement"):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)


if __name__ == "__main__":
    unittest.main()
