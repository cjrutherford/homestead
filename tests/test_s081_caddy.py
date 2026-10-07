"""S081 — Caddy Deployment conformance tests.

Guardrails for the edge proxy: a pinned Caddy image version of record,
ordinary Compose artifacts (compose.yaml, Caddyfile, routes snippet
directory) that run with vanilla `docker compose` and no Homestead at
runtime, admin endpoint kept container-local for reloads, deploy planned
as an idempotent operation (ensure network, write files, up, verify),
and no serving. Behavior is covered by Go tests in internal/docker
with stubbed execution.
"""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def read(rel: str) -> str:
    return (REPO / rel).read_text(encoding="utf-8")


class TestCaddyContract(unittest.TestCase):
    def test_pinned_image(self):
        src = read("internal/docker/caddy.go")
        for symbol in ("CaddyImage", '"caddy:2', "CaddyStackName",
                       '"caddy"', "func CaddyStack(", "func WriteStack(",
                       "func DeployDefinition("):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)

    def test_ordinary_artifacts(self):
        src = read("internal/docker/caddy.go")
        for symbol in ("compose.yaml", "Caddyfile", "routes",
                       "caddy-data", "caddy-config", "homestead-edge",
                       "external: true", "unless-stopped"):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)

    def test_local_admin(self):
        src = read("internal/docker/caddy.go")
        self.assertIn("admin localhost:2019", src)

    def test_idempotent_deploy(self):
        src = read("internal/docker/caddy.go")
        self.assertIn('"caddy.deploy"', src)
        for symbol in ("ensure-", "write-stack", '"up"', "verify"):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)

    def test_no_serving(self):
        src = read("internal/docker/caddy.go")
        for banned in ("net/http", "HandleFunc"):
            with self.subTest(banned=banned):
                self.assertNotIn(banned, src)


if __name__ == "__main__":
    unittest.main()
