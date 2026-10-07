"""S120 — Exposure Policy Engine conformance tests.

Guardrails for exposure evaluation: recommended versus selected
exposure graded per route with no network calls and no mutations,
least privilege governing ties with private names always resolving
local, warnings for unreachable-as-declared exposure and information
for narrowed or held-back posture, invalid routes failing instead of
grading into advice, and enforcement left to planners. Behavior is
covered by Go tests in internal/docker.
"""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def read(rel: str) -> str:
    return (REPO / rel).read_text(encoding="utf-8")


class TestEngineContract(unittest.TestCase):
    def test_engine_shapes(self):
        src = read("internal/docker/policyengine.go")
        for symbol in ("type Assessment", "type Finding",
                       "func Assess(", "func AssessAll(",
                       '"info"', '"warn"', "Recommended",
                       "Aligned"):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)


if __name__ == "__main__":
    unittest.main()
