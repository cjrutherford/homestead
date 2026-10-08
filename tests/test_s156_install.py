"""S156 — Install Operation conformance tests.

Guardrails for approved execution: per-directory provisioning plus
stack writing plus up as one runner-backed operation failing fast
with offenders named, empty plans and missing registries refused
before anything runs, production directories resolving from plans
with isolated overrides for tests, earlier steps surviving failed
ups, and compose execution behind a stubbed seam. Behavior is
covered by Go tests in internal/catalog with temporary mounts.
"""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def read(rel: str) -> str:
    return (REPO / rel).read_text(encoding="utf-8")


class TestInstallOpContract(unittest.TestCase):
    def test_operation_shapes(self):
        src = read("internal/catalog/operations.go")
        for symbol in ("func InstallDefinition(",
                       '"catalog.install"', "provision:",
                       "write-stack", "composeUp"):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)


if __name__ == "__main__":
    unittest.main()
