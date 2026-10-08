"""S158 — Removal Operation conformance tests.

Guardrails for uninstallation: down plus generated-file removal with
data preserved by default, explicit data removal naming every root
with escapes and mount roots refused and missing roots failing
loudly, ambiguous intents (roots without removal and vice versa)
refused, never-installed apps refused instead of no-op success, and
runner-backed execution failing fast. Behavior is covered by Go
tests in internal/catalog with temporary stacks.
"""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def read(rel: str) -> str:
    return (REPO / rel).read_text(encoding="utf-8")


class TestRemoveContract(unittest.TestCase):
    def test_remove_shapes(self):
        src = read("internal/catalog/remove.go")
        for symbol in ("type Removal", "type DataDir",
                       "func RemoveDefinition(",
                       '"catalog.remove"', "remove-data",
                       "not installed"):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)


if __name__ == "__main__":
    unittest.main()
