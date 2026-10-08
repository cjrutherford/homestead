"""S166 — Jellyfin Exposure conformance tests.

Guardrails for media posture: owner-selected origins with metadata
ceilings holding unless matching live overrides name the widening,
private names never going public, optional proxy narrowing, and
assessment plus recommendations riding along so enforcement never
travels without reasoning. Behavior is covered by Go tests in
internal/jellyfin with rendered enforcement proofs.
"""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def read(rel: str) -> str:
    return (REPO / rel).read_text(encoding="utf-8")


class TestJellyfinExposureContract(unittest.TestCase):
    def test_exposure_shapes(self):
        src = read("internal/jellyfin/exposure.go")
        for symbol in ("type Decision", "func Decide(",
                       "record an override", "cannot go public"):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)


if __name__ == "__main__":
    unittest.main()
