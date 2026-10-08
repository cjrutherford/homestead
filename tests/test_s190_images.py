"""S190 — Image Update Discovery conformance tests.

Guardrails for update detection: manifest pins compared against
running images with strays staying out and empty running sets
failing, registry tags filtered to newer same-variant versions with
numeric ordering, Docker Hub listings bounded with rate limits
surfacing, non-Hub images refused loudly, and registry outages
erroring instead of reading as current. Behavior is covered by Go
tests in internal/update against scripted registries.
"""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def read(rel: str) -> str:
    return (REPO / rel).read_text(encoding="utf-8")


class TestDiscoveryContract(unittest.TestCase):
    def test_discovery_shapes(self):
        src = read("internal/update/images.go")
        for symbol in ("func Drift(", "func NewerTags(",
                       "func RegistryTags(",
                       "func RegistryUpdates("):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)


if __name__ == "__main__":
    unittest.main()
