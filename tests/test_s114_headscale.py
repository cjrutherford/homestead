"""S114 — Headscale Deployment Profile conformance tests.

Guardrails for the managed control plane: a pinned Headscale image
version of record, ordinary Compose artifacts (compose.yaml with the
control port published plus config/data directories) that run with
vanilla `docker compose` and no Homestead at runtime, a minimal
config naming the public origin with container-local gRPC and SQLite
state while DNS stays at defaults for S115, an https-only server URL
with LAN http enrolling directly instead, deploy planned as an
idempotent operation, user bootstrap converging on existing names,
and no serving. Behavior is covered by Go tests in internal/docker
with stubbed execution.
"""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def read(rel: str) -> str:
    return (REPO / rel).read_text(encoding="utf-8")


class TestHeadscaleDeployContract(unittest.TestCase):
    def test_pinned_image(self):
        src = read("internal/docker/headscale.go")
        for symbol in ("HeadscaleImage", '"headscale/headscale:0',
                       "HeadscaleStackName", '"headscale"',
                       "func HeadscaleStack(",
                       "func WriteHeadscaleStack(",
                       "func HeadscaleDeployDefinition(",
                       "func CreateUserDefinition("):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)

    def test_ordinary_artifacts(self):
        src = read("internal/docker/headscale.go")
        for symbol in ("compose.yaml", "config/config.yaml", "server_url",
                       '"8080:8080"', "homestead-platform", "external: true",
                       '"users", "create"', '"headscale.deploy"',
                       '"headscale.user"'):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)

    def test_no_serving(self):
        src = read("internal/docker/headscale.go")
        for banned in ("net/http", "HandleFunc"):
            with self.subTest(banned=banned):
                self.assertNotIn(banned, src)


if __name__ == "__main__":
    unittest.main()
