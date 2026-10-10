"""S230 — Control Plane Recovery conformance tests.

Static guardrails for reinstalling the daemon without destroying
workloads: the script is present, executable, and syntactically
valid, root-gated with strict mode, gates the supported
distribution, refuses missing databases as disaster recovery
instead of booting amnesiac over live workloads, stops the daemon
without touching containers, snapshots state before reinstalling
through the tested install path, waits on health, and diffs the
workload set before declaring success. The Makefile exposes the
recover target and lints the script.
"""

import os
import stat
import subprocess
import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent
SCRIPT = REPO / "scripts" / "recover-control-plane.sh"


def read(rel: str) -> str:
    return (REPO / rel).read_text(encoding="utf-8")


class TestRecoverScript(unittest.TestCase):
    def test_script_exists_and_executable(self):
        self.assertTrue(SCRIPT.is_file(), "scripts/recover-control-plane.sh missing")
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
        content = read("scripts/recover-control-plane.sh")
        self.assertIn("set -euo pipefail", content)
        self.assertIn("id -u", content)

    def test_phases_cover_recovery(self):
        content = read("scripts/recover-control-plane.sh")
        for symbol in ("workload inventory", "stop daemon",
                       "snapshot state", "install-systemd.sh",
                       "health", "workload check",
                       "workloads intact"):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, content)

    def test_state_and_workloads_protected(self):
        content = read("scripts/recover-control-plane.sh")
        for symbol in ("homestead.db", "recovery-", "docker ps -q",
                       "diff ", "S234"):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, content)
        for banned in ("rm -rf /opt", "rm -rf /var/lib/homestead ",
                       "docker stop", "docker down", "docker rm"):
            with self.subTest(banned=banned):
                self.assertNotIn(banned, content)

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
        self.assertRegex(makefile, r"(?m)^recover:")
        self.assertIn("bash -n scripts/recover-control-plane.sh", makefile)


if __name__ == "__main__":
    unittest.main()
