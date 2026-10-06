"""S031 — Embedded Dashboard Assets conformance tests.

Guardrails for serving the production UI from the daemon: the serving
logic is tag-free and hermetic, the embed split keeps plain Go builds fast
while the appliance build carries the UI, the embed copy is generated
(never committed), the catch-all never shadows the API, and the daemon
mounts the UI only when embedded. Runtime behavior is covered by Go
tests in internal/dashboard and internal/server.
"""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def read(rel: str) -> str:
    return (REPO / rel).read_text(encoding="utf-8")


class TestEmbedSplit(unittest.TestCase):
    def test_embed_file_tagged(self):
        src = read("internal/dashboard/embed.go")
        self.assertIn("//go:build embeddash", src)
        self.assertIn("//go:embed dist", src)
        self.assertIn("func Embedded()", src)

    def test_noembed_stub(self):
        src = read("internal/dashboard/noembed.go")
        self.assertIn("//go:build !embeddash", src)
        self.assertIn("func Embedded()", src)

    def test_serving_logic_untagged(self):
        src = read("internal/dashboard/dashboard.go")
        self.assertNotIn("go:build", src)
        self.assertNotIn("go:embed", src)
        self.assertIn("index.html", src)
        self.assertIn("no-cache", src)

    def test_embed_copy_gitignored(self):
        self.assertIn("internal/dashboard/dist/", read(".gitignore"))


class TestWiring(unittest.TestCase):
    def test_server_mount(self):
        src = read("internal/server/dashboard.go")
        self.assertIn("func (s *Server) MountDashboard(", src)
        self.assertIn('"/"', src.replace("'", '"'))

    def test_daemon_mounts_when_embedded(self):
        main = read("cmd/homestead-server/main.go")
        self.assertIn("dashboard.Embedded()", main)
        self.assertIn("MountDashboard", main)
        self.assertIn("not embedded", main)

    def test_makefile_embed_target(self):
        makefile = read("Makefile")
        self.assertIn("dashboard-embed:", makefile)
        self.assertIn("internal/dashboard/dist", makefile)

    def test_ci_builds_appliance_binary(self):
        ci = read(".github/workflows/ci.yml")
        self.assertIn("dashboard-embed", ci)
        self.assertIn("embeddash", ci)


if __name__ == "__main__":
    unittest.main()
