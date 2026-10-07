"""S121 — Exposure Planner conformance tests.

Guardrails for exposure planning: one offline plan per route across
DNS, TLS, firewall, and edge with an embedded engine assessment,
private names refused public widening, public records requiring a
known address under the zone origin, TLS work derived from coverage
plus monitoring (healthy none, expiring renew, otherwise issue,
manual never renewed), firewall desired as unexpanded policies for
live expansion at apply, edge carrying the target exposure with
declared TLS untouched, and no-ops planning empty. Behavior is
covered by Go tests in internal/exposure.
"""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def read(rel: str) -> str:
    return (REPO / rel).read_text(encoding="utf-8")


class TestPlannerContract(unittest.TestCase):
    def test_planner_shapes(self):
        src = read("internal/exposure/planner.go")
        for symbol in ("type Plan", "type Request",
                       "func PlanExposure(", "type CertificatePlan",
                       '"none"', '"issue"', '"renew"',
                       "cannot go public"):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)


if __name__ == "__main__":
    unittest.main()
