"""S220 — Bootstrap Script conformance tests.

Static guardrails for turning clean Debian/Ubuntu into an installed
native kernel: the script is present, executable, and syntactically
valid, root-gated with strict mode, refuses non-Debian/Ubuntu
distributions, requires systemd and apt, installs only minimal OS
packages, drives the Go toolchain from go.mod with checksum-verified
official binaries (never distribution packages), and finishes
through the existing systemd install. The Makefile exposes the
bootstrap target and lints the script.
"""

import os
import stat
import subprocess
import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent
SCRIPT = REPO / "scripts" / "bootstrap.sh"


def read(rel: str) -> str:
    return (REPO / rel).read_text(encoding="utf-8")


class TestBootstrapScript(unittest.TestCase):
    def test_script_exists_and_executable(self):
        self.assertTrue(SCRIPT.is_file(), "scripts/bootstrap.sh missing")
        self.assertTrue(SCRIPT.stat().st_mode & stat.S_IXUSR)

    def test_script_passes_bash_syntax_check(self):
        proc = subprocess.run(
            ["bash", "-n", str(SCRIPT)],
            capture_output=True,
            text=True,
            timeout=30,
        )
        self.assertEqual(proc.returncode, 0, proc.stderr)

    def test_script_requires_root_and_strict_mode(self):
        content = read("scripts/bootstrap.sh")
        self.assertIn("set -euo pipefail", content)
        self.assertIn("id -u", content)

    def test_refuses_foreign_distributions(self):
        content = read("scripts/bootstrap.sh")
        for symbol in ("/etc/os-release", "debian", "ubuntu",
                       "unsupported distribution"):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, content)

    def test_minimal_dependencies(self):
        content = read("scripts/bootstrap.sh")
        for symbol in ("command -v systemctl", "ca-certificates curl nftables",
                       "go.mod", "dl.google.com/go", "sha256sum -c"):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, content)

    def test_finishes_through_systemd_install(self):
        content = read("scripts/bootstrap.sh")
        self.assertIn("scripts/install-systemd.sh", content)

    def test_refuses_without_root(self):
        if os.geteuid() == 0:
            self.skipTest("running as root")
        proc = subprocess.run(
            ["bash", str(SCRIPT)],
            capture_output=True,
            text=True,
            timeout=30,
        )
        self.assertEqual(proc.returncode, 1)
        self.assertIn("run as root", proc.stderr)

    def test_makefile_targets(self):
        makefile = read("Makefile")
        self.assertRegex(makefile, r"(?m)^bootstrap:")
        self.assertIn("bash -n scripts/bootstrap.sh", makefile)


if __name__ == "__main__":
    unittest.main()
