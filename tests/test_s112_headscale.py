"""S112 — Headscale Adapter conformance tests.

Guardrails for Headscale-controlled clients: server origins validate
as http/https with a host, enrolment pins the shared up flow to the
control plane without a second client implementation, observation
reuses the identical status mapping (same daemon, same JSON, no parser
to drift), login servers validate at intent time, and pre-auth plus
API key secrets share one declared spelling for later slices.
Behavior is covered by Go tests in internal/overlay with stubbed
execution.
"""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def read(rel: str) -> str:
    return (REPO / rel).read_text(encoding="utf-8")


class TestHeadscaleContract(unittest.TestCase):
    def test_adapter_shapes(self):
        src = read("internal/overlay/headscale.go")
        for symbol in ("type HeadscaleServer", "func HeadscaleUp(",
                       "func HeadscaleStatus(", "overlay_headscale_preauth",
                       "overlay_headscale_apikey"):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)

    def test_shared_client(self):
        src = read("internal/overlay/tailscale.go")
        self.assertIn("LoginServer", src)
        self.assertIn("--login-server", src)
        self.assertIn("login server must be", src)


if __name__ == "__main__":
    unittest.main()
