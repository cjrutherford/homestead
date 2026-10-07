"""S124 — Safety Recommendations conformance tests.

Guardrails for displayed counsel: engine, package, and certificate
sources merged with warns before information, ceiling breaches
advising audited overrides instead of refusing, plaintext-public and
unhealthy certificates warning, quiet postures confirming explicitly,
rendered text quoted verbatim by presentation layers, and invalid
routes failing instead of advising. Behavior is covered by Go tests
in internal/exposure.
"""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def read(rel: str) -> str:
    return (REPO / rel).read_text(encoding="utf-8")


class TestRecommendContract(unittest.TestCase):
    def test_recommend_shapes(self):
        src = read("internal/exposure/recommendations.go")
        for symbol in ("func Recommend(", "func Format(",
                       "type Recommendation", '"engine"',
                       '"package"', '"certificate"',
                       "audited override"):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)


if __name__ == "__main__":
    unittest.main()
