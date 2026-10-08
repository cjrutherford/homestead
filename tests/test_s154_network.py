"""S154 — Network Recommendations conformance tests.

Guardrails for route defaults: per-endpoint recommendations with
service linkage, exposure from declarations, hostname-agnostic TLS
hints, and warnings carried alongside; concretization with owner
hostname and upstream inputs validating fully; ceiling lookups for
planners; and invalid metadata failing instead of recommending.
Behavior is covered by Go tests in internal/catalog.
"""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def read(rel: str) -> str:
    return (REPO / rel).read_text(encoding="utf-8")


class TestNetworkContract(unittest.TestCase):
    def test_recommend_shapes(self):
        src = read("internal/catalog/networks.go")
        for symbol in ("func RecommendRoutes(", "func BuildRoute(",
                       "func ExposureFor(", "type RouteRecommendation"):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)


if __name__ == "__main__":
    unittest.main()
