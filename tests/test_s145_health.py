"""S145 — Storage Health conformance tests.

Guardrails for mount tracking: per-mount grades with evidence for
presence, writability, and space, degraded covering read-only,
stale, and low-space mounts, missing shared deliberately between
loss and never-existed, roll-ups mirroring the worst status, and
checks pure against facts with stores staying outside. Behavior is
covered by Go tests in internal/storage against fixture discovery.
"""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def read(rel: str) -> str:
    return (REPO / rel).read_text(encoding="utf-8")


class TestHealthContract(unittest.TestCase):
    def test_health_shapes(self):
        src = read("internal/storage/health.go")
        for symbol in ("func CheckMount(", "func CheckAll(",
                       "type MountHealth", "type Summary",
                       '"healthy"', '"degraded"', '"missing"'):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)


if __name__ == "__main__":
    unittest.main()
