"""S163 — Jellyfin Route conformance tests.

Guardrails for the media route: default hostnames under base
domains, routes built from catalog metadata with recommended
exposure and internal TLS plus the public-info health probe,
rendering through the shared generator, and invalid inputs refused
with widening left to ceremony. Behavior is covered by Go tests in
internal/jellyfin against the curated manifest.
"""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def read(rel: str) -> str:
    return (REPO / rel).read_text(encoding="utf-8")


class TestJellyfinRouteContract(unittest.TestCase):
    def test_route_shapes(self):
        src = read("internal/jellyfin/route.go")
        for symbol in ("func DefaultHostname(", "func RouteFor(",
                       "/System/Info/Public"):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)


if __name__ == "__main__":
    unittest.main()
