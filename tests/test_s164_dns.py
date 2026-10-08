"""S164 — Jellyfin DNS conformance tests.

Guardrails for media naming: validated LAN rewrites applied
idempotently through the provider adapter, resolution verified to
the bound answer, split-horizon public records planned validated
but unapplied with application owned by exposure flows, and no
proxy coupling in the DNS layer. Behavior is covered by Go tests
in internal/jellyfin against doubles and fakes.
"""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def read(rel: str) -> str:
    return (REPO / rel).read_text(encoding="utf-8")


class TestJellyfinDNSContract(unittest.TestCase):
    def test_dns_shapes(self):
        src = read("internal/jellyfin/dns.go")
        for symbol in ("func LocalRewrite(", "func ApplyLocalName(",
                       "func VerifyLocalName(", "func PublicRecord("):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)


if __name__ == "__main__":
    unittest.main()
