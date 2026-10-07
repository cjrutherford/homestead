"""S113 — Existing Headscale Setup conformance tests.

Guardrails for external-server enrolment: setup shape validated
(server origin, user spelling, hostname, routable routes), empty
pre-auth keys refused outright, enrolment running up against the
server plus a verify proving online-with-addresses, offline results
aborting the operation, and keys arriving per call from secrets
rather than stored state. Behavior is covered by Go tests in
internal/overlay with stubbed execution.
"""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def read(rel: str) -> str:
    return (REPO / rel).read_text(encoding="utf-8")


class TestExistingContract(unittest.TestCase):
    def test_enrol_shapes(self):
        src = read("internal/overlay/existing.go")
        for symbol in ("type ExistingSetup", "func EnrolDefinition(",
                       '"headscale.enrol"', "pre-auth key must not be empty",
                       "not online after enrolment"):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)


if __name__ == "__main__":
    unittest.main()
