"""S157 — Configure Operation conformance tests.

Guardrails for post-install configuration: reviewed per-service
set/unset plans with known services and well-formed keys, layered
override files merging with existing state and deleting empty,
0600 files with operational-only values, base compose files staying
generated-only, and reviewed plans executing through write plus up.
Behavior is covered by Go tests in internal/catalog with temporary
stacks.
"""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def read(rel: str) -> str:
    return (REPO / rel).read_text(encoding="utf-8")


class TestConfigureContract(unittest.TestCase):
    def test_configure_shapes(self):
        src = read("internal/catalog/configure.go")
        for symbol in ("type ConfigPlan", "type ConfigChange",
                       "func ApplyConfig(", "func ConfigureDefinition(",
                       '"catalog.configure"',
                       "compose.override.yaml"):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)


if __name__ == "__main__":
    unittest.main()
