"""S246 — Tunnel Provider conformance tests.

Guardrails for tunnel remote access over the API and CLI: discovery
prefers automated router protocols and falls through to the tunnel
when the router stays silent, setup seals the API token and opens an
operation without echoing credentials, teardown needs a credential on
file plus an explicit account, and status never leaks secrets.
Behavior is covered by Go tests in internal/router,
internal/server, and cmd/homesteadctl.
"""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def read(rel: str) -> str:
    return (REPO / rel).read_text(encoding="utf-8")


class TestTunnelProvider(unittest.TestCase):
    def test_api_shapes(self):
        src = read("internal/server/tunnel.go")
        for symbol in ("GET /api/v1/tunnel/status",
                       "POST /api/v1/tunnel/setup",
                       "DELETE /api/v1/tunnel/tunnels/{id}",
                       "func (s *Server) MountTunnel(",
                       "TunnelSecretName"):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)

    def test_cli_shapes(self):
        src = read("cmd/homesteadctl/tunnel.go")
        for symbol in ('"status"', '"setup"', '"teardown"',
                       "--hostname", "--service", "--api-token",
                       "--account-id", "--follow"):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)

    def test_provider_shapes(self):
        src = read("internal/router/tunnel.go")
        for symbol in ("func PreferredProvider(",
                       "func DefaultRoute(",
                       "func (c *TunnelClient) CreateTunnel(",
                       "func (c *TunnelClient) DeleteTunnel(",
                       "func (c *TunnelClient) GetTunnel(",
                       "func (c *TunnelClient) ListAccounts(",
                       "func RenderConnectorConfig("):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)

    def test_parity_rows(self):
        matrix = read("docs/PARITY.md")
        for symbol in ("| /api/v1/tunnel/status |",
                       "| /api/v1/tunnel/setup |",
                       "| /api/v1/tunnel/tunnels/{id} |"):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, matrix)


if __name__ == "__main__":
    unittest.main()
