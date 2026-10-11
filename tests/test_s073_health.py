"""S073 — Docker Health API conformance tests.

Guardrails for read-only Engine health: versions plus image, container,
and network inventory over the Unix socket with stdlib HTTP (no SDK),
served on one endpoint, printed by the CLI — with unreachable daemons
reported as data. Lifecycle stays in the Compose CLI (S074/S075).
Behavior is covered by Go tests in internal/docker, internal/server,
internal/apiclient, and cmd/homesteadctl.
"""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent
CTL = REPO / "cmd" / "homesteadctl"


def read(rel: str) -> str:
    return (REPO / rel).read_text(encoding="utf-8")


def ctl_sources() -> str:
    return "\n".join(
        p.read_text(encoding="utf-8") for p in CTL.glob("*.go") if not p.name.endswith("_test.go")
    )


class TestHealthContract(unittest.TestCase):
    def test_client_surface(self):
        src = read("internal/docker/health.go")
        for symbol in ("func NewClient(", "func SocketPath(", "func (c *Client) Health(",
                       "type Health struct", "type Container struct",
                       "type Image struct", "type Network struct"):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)

    def test_read_only_unix_socket(self):
        src = read("internal/docker/health.go")
        self.assertIn('"unix"', src)
        self.assertIn("http.MethodGet", src)
        for banned in ("MethodPost", "MethodPut", "MethodDelete", "docker/docker"):
            with self.subTest(banned=banned):
                self.assertNotIn(banned, src)

    def test_endpoint_and_client(self):
        self.assertIn("GET /api/v1/docker/health", read("internal/server/docker.go"))
        self.assertIn("MountDocker", read("internal/server/mount.go"))
        self.assertIn("func (c *Client) DockerHealth(", read("internal/apiclient/docker.go"))

    def test_cli_command(self):
        src = ctl_sources()
        for cmd in ('"docker"', "runDockerHealth"):
            with self.subTest(cmd=cmd):
                self.assertIn(cmd, src)

    def test_parity_row(self):
        parity = read("docs/PARITY.md")
        self.assertIn("| GET | /api/v1/docker/health |", parity)


if __name__ == "__main__":
    unittest.main()
