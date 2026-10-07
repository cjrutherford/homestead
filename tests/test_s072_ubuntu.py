"""S072 — Ubuntu Docker Installation conformance tests.

Guardrails for distro isolation: Ubuntu detection with its own identity,
codename, and repository URLs, sharing only the S071 InstallDefinition
shape — Debian and Ubuntu paths must never cross. Behavior is covered
by Go tests in internal/docker.
"""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def read(rel: str) -> str:
    return (REPO / rel).read_text(encoding="utf-8")


class TestUbuntuContract(unittest.TestCase):
    def test_ubuntu_entry_point(self):
        src = read("internal/docker/install.go")
        self.assertIn("func UbuntuDistro()", src)
        self.assertIn("func DebianDistro()", src)

    def test_shared_definition_shape(self):
        """One InstallDefinition serves both distros via Distro values."""
        src = read("internal/docker/install.go")
        self.assertEqual(src.count("func InstallDefinition("), 1)

    def test_isolated_urls(self):
        src = read("internal/docker/install.go")
        self.assertIn("download.docker.com/linux/\" + id + \"/gpg", src)


if __name__ == "__main__":
    unittest.main()
