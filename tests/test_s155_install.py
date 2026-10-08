"""S155 — Install Planner conformance tests.

Guardrails for install planning: one offline plan across
requirements, routes, compose, DNS, TLS, firewall, and directories
with lifecycle verbs, unmet requirements and unknown inputs
refusing before anything writes, public routes needing zone and
address, TLS strategies through shared eligibility, firewall scoped
to widest exposure, and directories joined from class mounts.
Behavior is covered by Go tests in internal/catalog.
"""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def read(rel: str) -> str:
    return (REPO / rel).read_text(encoding="utf-8")


class TestInstallContract(unittest.TestCase):
    def test_planner_shapes(self):
        src = read("internal/catalog/install.go")
        for symbol in ("type InstallRequest", "type InstallPlan",
                       "func PlanInstall(", "type TLSNeed",
                       "type ProvisionDir", '"provision"', '"up"',
                       "requirements unmet"):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)


if __name__ == "__main__":
    unittest.main()
