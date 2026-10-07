"""S111 — Tailscale Adapter conformance tests.

Guardrails for existing Tailscale access: absence is data, never
breakage; live tailnets map from status JSON with backend state
driving presence, peers sorted with invalid names skipped and absent
online flags defaulting present, peer self-addresses excluded from
offers while subnet and exit offers stay; up converges login and
settings in one runner-backed step advertising enabled routes only
with keys arriving per call; logout is the unambiguous off state; and
a labeled demo profile previews the UX without enrollment. Behavior
is covered by Go tests in internal/overlay with stubbed execution.
"""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def read(rel: str) -> str:
    return (REPO / rel).read_text(encoding="utf-8")


class TestTailscaleContract(unittest.TestCase):
    def test_adapter_shapes(self):
        src = read("internal/overlay/tailscale.go")
        for symbol in ("func Present(", "func Status(",
                       "func UpDefinition(", "func LogoutDefinition(",
                       "func DemoConfig(", "func DemoNetwork(",
                       '"tailscale.up"', '"tailscale.logout"',
                       "overlay_tailscale_authkey"):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)

    def test_adoption_rules(self):
        src = read("internal/overlay/tailscale.go")
        for symbol in ("ErrTailscaleAbsent", "BackendState",
                       "--advertise-routes", "--accept-routes",
                       "--accept-dns"):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)


if __name__ == "__main__":
    unittest.main()
