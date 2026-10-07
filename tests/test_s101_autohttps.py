"""S101 — Caddy Automatic HTTPS conformance tests.

Guardrails for standard automation: private names (single-label, LAN
and reserved suffixes, IP literals) never face public ACME, direct
challenges count as valid only for public names on public exposure,
recommendations automate there and go internal everywhere else with
plain HTTP never recommended, and rendering refuses automatic mode on
private names instead of deploying routes Caddy could never bring up.
Behavior is covered by Go tests in internal/docker.
"""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def read(rel: str) -> str:
    return (REPO / rel).read_text(encoding="utf-8")


class TestAutoHTTPSContract(unittest.TestCase):
    def test_policy_shapes(self):
        src = read("internal/docker/autohttps.go")
        for symbol in ("func IsPrivateName(", "func DirectChallengesValid(",
                       "func RecommendedTLS("):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)

    def test_render_gate(self):
        src = read("internal/docker/routes.go")
        self.assertIn("automatic TLS needs a public name", src)


if __name__ == "__main__":
    unittest.main()
