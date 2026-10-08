"""S172 — Recommended Base Suite conformance tests.

Guardrails for the bundle: a curated ordered member list resolved
from loaded catalogs with missing members failing, joint grading
with per-package verdicts, combined memory footprints against host
capacity, cross-member host-port collisions naming both owners, and
invalid manifests failing instead of grading. Behavior is covered
by Go tests in internal/catalog against the shipped catalog.
"""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def read(rel: str) -> str:
    return (REPO / rel).read_text(encoding="utf-8")


class TestSuiteContract(unittest.TestCase):
    def test_suite_shapes(self):
        src = read("internal/catalog/suite.go")
        for symbol in ("func LoadSuite(", "func CheckSuite(",
                       "type SuiteReport", "RecommendedSuite",
                       "claimed by both", "combined memory"):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)


if __name__ == "__main__":
    unittest.main()
