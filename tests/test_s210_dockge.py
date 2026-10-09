"""S210 — Dockge Deployment conformance tests.

Guardrails for the expert interface: a pinned Dockge image version of
record, ordinary Compose artifacts (compose.yaml plus data directory)
that run with vanilla `docker compose` and no Homestead at runtime,
the engine socket and stacks directory mounted with matching
host/container paths, the UI port published for the owner with access
policy left to the administration link, root file ownership, no
generated credentials (login on first start), deploy planned as an
idempotent operation (ensure network, write files, up, verify), and no
serving. Behavior is covered by Go tests in internal/docker with
stubbed execution.
"""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def read(rel: str) -> str:
    return (REPO / rel).read_text(encoding="utf-8")


class TestDockgeContract(unittest.TestCase):
    def test_pinned_image(self):
        src = read("internal/docker/dockge.go")
        for symbol in ("DockgeImage", '"louislam/dockge:1',
                       "DockgeStackName", '"dockge"',
                       "func DockgeStack(", "func WriteDockgeStack(",
                       "func DockgeDeployDefinition("):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)

    def test_ordinary_artifacts(self):
        src = read("internal/docker/dockge.go")
        for symbol in ("compose.yaml", '"5001:5001/tcp"',
                       "/var/run/docker.sock:/var/run/docker.sock",
                       "./data:/app/data", "DOCKGE_STACKS_DIR=",
                       "homestead-platform", "external: true",
                       "unless-stopped", '"dockge.deploy"'):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)

    def test_no_serving(self):
        src = read("internal/docker/dockge.go")
        for banned in ("net/http", "HandleFunc"):
            with self.subTest(banned=banned):
                self.assertNotIn(banned, src)


if __name__ == "__main__":
    unittest.main()
