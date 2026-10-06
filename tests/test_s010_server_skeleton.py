"""S010 — homestead-server Skeleton conformance tests.

Static guardrails for the daemon skeleton: the server package exposes only
the bootstrap-stage routes (health/version per ADR-015), binds loopback by
default (ADR-006), drains gracefully on shutdown, and the command wires
SIGINT/SIGTERM handling. Runtime behavior is covered by Go tests in
internal/server.
"""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def read(rel: str) -> str:
    return (REPO / rel).read_text(encoding="utf-8")


class TestServerSkeleton(unittest.TestCase):
    def test_server_package_exists(self):
        self.assertTrue((REPO / "internal" / "server" / "server.go").is_file())
        self.assertTrue((REPO / "internal" / "server" / "server_test.go").is_file())

    def test_only_bootstrap_routes_registered(self):
        """Unclaimed daemons expose health/version only — no admin API yet."""
        src = read("internal/server/server.go")
        self.assertIn("GET /healthz", src)
        self.assertIn("GET /version", src)
        self.assertNotIn("/admin", src)
        self.assertNotIn("/api/", src)

    def test_default_bind_is_loopback(self):
        src = read("internal/server/server.go")
        self.assertIn('"127.0.0.1:', src)

    def test_graceful_shutdown_wired(self):
        src = read("internal/server/server.go")
        self.assertIn(".Shutdown(", src)
        main = read("cmd/homestead-server/main.go")
        self.assertIn("signal.NotifyContext", main)
        self.assertIn("SIGINT", main)
        self.assertIn("SIGTERM", main)

    def test_main_delegates_to_server_package(self):
        main = read("cmd/homestead-server/main.go")
        self.assertIn("homestead/internal/server", main)
        self.assertIn("server.New(", main)


if __name__ == "__main__":
    unittest.main()
