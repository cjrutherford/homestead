"""S080 — Platform Networks conformance tests.

Guardrails for shared Docker networks: the edge and platform set with
managed labels, idempotent per-network ensure plus closing verify
through the operation engine, driver mismatches refused instead of
touched, and no serving. Behavior is covered by Go tests in
internal/docker with stateful stubbed execution.
"""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def read(rel: str) -> str:
    return (REPO / rel).read_text(encoding="utf-8")


class TestNetworksContract(unittest.TestCase):
    def test_standard_set(self):
        src = read("internal/docker/networks.go")
        for symbol in ("NetworkEdge", "NetworkPlatform", '"homestead-edge"',
                       '"homestead-platform"', "func StandardNetworks(",
                       "func EnsureDefinition(", "ManagedLabel"):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)

    def test_idempotent_ensure(self):
        src = read("internal/docker/networks.go")
        self.assertIn("no such network", src)
        self.assertIn("foreign configuration", src)
        self.assertIn('"network", "create"', src)

    def test_no_serving(self):
        src = read("internal/docker/networks.go")
        for banned in ("net/http", "HandleFunc"):
            with self.subTest(banned=banned):
                self.assertNotIn(banned, src)


if __name__ == "__main__":
    unittest.main()
