"""S071 — Debian Docker Installation conformance tests.

Guardrails for the first operation producer: a planned Docker CE plus
Compose v2 install for Debian-family distros, executed step by step
through the S061 runner with per-step idempotent skips and a closing
verify. No new dependencies (stdlib exec plus fetch); surfacing belongs
to the bootstrap (S220) and future Docker UX. Behavior is covered by Go
tests in internal/docker with stubbed execution.
"""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def read(rel: str) -> str:
    return (REPO / rel).read_text(encoding="utf-8")


class TestInstallContract(unittest.TestCase):
    def test_entry_points(self):
        src = read("internal/docker/install.go")
        for symbol in ("func InstallDefinition(", "func DebianDistro(",
                       "func ReleaseIdentity(", "type Distro struct"):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)

    def test_four_steps(self):
        src = read("internal/docker/install.go")
        for step in ("ensure-prerequisites", "configure-repository",
                     "install-packages", "verify"):
            with self.subTest(step=step):
                self.assertIn(f'"{step}"', src)

    def test_supported_packages(self):
        src = read("internal/docker/install.go")
        for package in ("docker-ce", "docker-ce-cli", "containerd.io",
                        "docker-buildx-plugin", "docker-compose-plugin"):
            with self.subTest(package=package):
                self.assertIn(package, src)

    def test_no_new_dependencies(self):
        self.assertNotIn("apt-go", read("go.mod"))

    def test_no_serving(self):
        src = read("internal/docker/install.go")
        for banned in ("net/http.ListenAndServe", "HandleFunc"):
            with self.subTest(banned=banned):
                self.assertNotIn(banned, src)


if __name__ == "__main__":
    unittest.main()
