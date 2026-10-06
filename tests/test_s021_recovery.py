"""S021 — Local Admin Recovery conformance tests.

Guardrails for break-glass recovery: the daemon serves a Unix socket with
a root-only mode, the recover endpoint lives on the socket mux and nowhere
else, recovery resets passwords without claim or session gates, the CLI
speaks the socket transport, and enforcement boundaries hold (no new
schema, no session creation here). Runtime behavior is covered by Go
tests in internal/auth, internal/server, internal/apiclient, and
cmd/homesteadctl.
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


class TestUnixSocket(unittest.TestCase):
    def test_socket_listener(self):
        src = read("internal/server/server.go")
        self.assertIn("DefaultSocketPath", src)
        self.assertIn("/run/homestead/homestead.sock", src)
        self.assertIn("func (s *Server) RunUnix(", src)
        self.assertIn("0o600", src)

    def test_socket_mux_separate(self):
        src = read("internal/server/server.go")
        self.assertIn("socketMux", src)
        self.assertIn("SocketHandler", src)

    def test_recover_socket_only(self):
        tcp_mounts = read("internal/server/auth.go")
        self.assertNotIn("admins/recover", tcp_mounts)
        recover_mount = read("internal/server/recover.go")
        self.assertIn("socketMux", recover_mount)
        self.assertIn("POST /api/v1/admins/recover", recover_mount)

    def test_daemon_serves_both(self):
        main = read("cmd/homestead-server/main.go")
        self.assertIn("RunUnix", main)
        self.assertIn('"socket"', main)


class TestRecoverFlow(unittest.TestCase):
    def test_service_resets_without_gates(self):
        import re

        code = re.sub(r"(?m)^\s*//.*$", "", read("internal/auth/recover.go"))
        self.assertIn("func (s *Service) Recover(", code)
        self.assertIn("ErrAdminNotFound", code)
        self.assertNotIn("CurrentState", code)
        self.assertNotIn("Verify(", code)

    def test_client_unix_transport(self):
        src = read("internal/apiclient/recover.go")
        self.assertIn("func UnixClient(", src)
        self.assertIn('"unix"', src)
        self.assertIn("func (c *Client) Recover(", src)

    def test_cli_recover_over_socket(self):
        import re

        code = re.sub(r"(?m)^\s*//.*$", "", ctl_sources())
        self.assertIn('"recover"', code)
        self.assertIn('"socket"', code)
        self.assertIn("UnixClient", code)

    def test_no_schema_change(self):
        src = read("internal/store/store.go")
        self.assertIn("CurrentVersion = 3", src)


if __name__ == "__main__":
    unittest.main()
