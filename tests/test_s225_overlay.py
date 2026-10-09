"""S225 — Optional Overlay Bootstrap conformance tests.

Guardrails for reaching OVERLAY_READY: the wizard selection
names the provider, live connectivity validates with node
addresses resolving, the overlay view intent validates against
the zone without applying anywhere, local-only selections skip
with a recorded none, re-runs converge, and refusals record
nothing. Behavior is covered by Go tests in internal/overlay
over schema v18.
"""

import re
import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def read(rel: str) -> str:
    return (REPO / rel).read_text(encoding="utf-8")


class TestOverlayBootstrap(unittest.TestCase):
    def test_bootstrap_shapes(self):
        src = read("internal/overlay/bootstrap.go")
        for symbol in ("type Readiness", "type State",
                       "func EstablishOverlay(", "func CurrentOverlay(",
                       "ErrNoOverlay", "AudienceOverlay",
                       "ValidateConnectivity("):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)

    def test_view_intent_not_application(self):
        src = read("internal/overlay/bootstrap.go")
        self.assertIn("overlayView(", src)
        self.assertNotIn("ApplyChanges(", src)

    def test_schema_v18(self):
        src = read("internal/store/store.go")
        version = int(re.search(r"(?m)^const CurrentVersion = (\d+)$", src).group(1))
        self.assertGreaterEqual(version, 18)
        self.assertIn("CREATE TABLE IF NOT EXISTS overlay_state", src)


if __name__ == "__main__":
    unittest.main()
