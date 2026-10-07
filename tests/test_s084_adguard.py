"""S084 — AdGuard Home Deployment conformance tests.

Guardrails for local DNS: a pinned AdGuard Home image version of record,
ordinary Compose artifacts (compose.yaml plus work/conf data
directories) that run with vanilla `docker compose` and no Homestead at
runtime, DNS on 53/tcp+udp with the admin/API port published for the
provider adapter, attachment to the platform network, no generated
configuration (defaults on first start, setup via the adapter), deploy
planned as an idempotent operation (ensure network, write files, up,
verify), and no serving. Behavior is covered by Go tests in
internal/docker with stubbed execution.
"""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def read(rel: str) -> str:
    return (REPO / rel).read_text(encoding="utf-8")


class TestAdguardContract(unittest.TestCase):
    def test_pinned_image(self):
        src = read("internal/docker/adguard.go")
        for symbol in ("AdguardImage", '"adguard/adguardhome:v',
                       "AdguardStackName", '"adguard"',
                       "func AdguardStack(", "func WriteAdguardStack(",
                       "func AdguardDeployDefinition("):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)

    def test_ordinary_artifacts(self):
        src = read("internal/docker/adguard.go")
        for symbol in ("compose.yaml", '"53:53/tcp"', '"53:53/udp"',
                       '"3000:3000/tcp"', "/opt/adguardhome/work",
                       "/opt/adguardhome/conf", "homestead-platform",
                       "external: true", "unless-stopped",
                       '"adguard.deploy"'):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)

    def test_no_serving(self):
        src = read("internal/docker/adguard.go")
        for banned in ("net/http", "HandleFunc"):
            with self.subTest(banned=banned):
                self.assertNotIn(banned, src)


if __name__ == "__main__":
    unittest.main()
