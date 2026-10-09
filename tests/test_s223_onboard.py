"""S223 — Domain Onboarding conformance tests.

Guardrails for connecting the customer domain: onboarding gates
on the security baseline with failing legs named, proves provider
ownership through the domain flow, records the validated plan as
the single connected domain under the acting owner, re-runs
converge by replacing, failed gates and proofs record nothing,
and actorless attempts refuse. Behavior is covered by Go tests in
internal/onboard over schema v16.
"""

import re
import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def read(rel: str) -> str:
    return (REPO / rel).read_text(encoding="utf-8")


class TestDomainOnboarding(unittest.TestCase):
    def test_onboarding_shapes(self):
        src = read("internal/onboard/onboard.go")
        for symbol in ("type Onboarded", "type Service",
                       "func New(", "func (o *Service) Baseline(",
                       "func (o *Service) Onboard(",
                       "func (o *Service) Current(",
                       "ErrNotOnboarded", "security baseline incomplete"):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)

    def test_no_new_surfaces(self):
        matrix = read("docs/PARITY.md")
        self.assertNotIn("onboard", matrix.lower())

    def test_schema_v16(self):
        src = read("internal/store/store.go")
        version = int(re.search(r"(?m)^const CurrentVersion = (\d+)$", src).group(1))
        self.assertGreaterEqual(version, 16)
        self.assertIn("CREATE TABLE IF NOT EXISTS domain_onboarding", src)


if __name__ == "__main__":
    unittest.main()
