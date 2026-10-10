"""S228 — Public Validation Bootstrap conformance tests.

Guardrails for reaching PUBLIC_VALIDATED: validation gates on a
public-target approval (unapproved and mismatched hosts refuse),
runs the five DNS/port/TLS/response layers without
short-circuiting, trips when management names resolve to the
public address, records every verdict with failed checks named,
and reads the last word back for status surfaces. Behavior is
covered by Go tests in internal/exposure over schema v21.
"""

import re
import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def read(rel: str) -> str:
    return (REPO / rel).read_text(encoding="utf-8")


class TestPublicValidation(unittest.TestCase):
    def test_verdict_shapes(self):
        src = read("internal/exposure/public.go")
        for symbol in ("type PublicValidation",
                       "func ValidatePublic(",
                       "func CurrentValidation(",
                       "ErrNoValidation", "ManagementClean",
                       "management:"):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)

    def test_gate_and_guard(self):
        src = read("internal/exposure/public.go")
        for symbol in ("not public", "is not approved host",
                       "managementExposed("):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)

    def test_schema_v21(self):
        src = read("internal/store/store.go")
        version = int(re.search(r"(?m)^const CurrentVersion = (\d+)$", src).group(1))
        self.assertGreaterEqual(version, 21)
        self.assertIn("CREATE TABLE IF NOT EXISTS public_validation", src)


if __name__ == "__main__":
    unittest.main()
