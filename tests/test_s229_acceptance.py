"""S229 — Bare-metal Acceptance conformance tests.

Static guardrails for the end-to-end acceptance run: the script is
present, executable, and syntactically valid, root-gated with
strict mode, gates the supported distribution, asserts
preconditions instead of installing past them, walks claim, admin,
doctor, firewall, and the base suite through supported surfaces
only (review-then-install per package), proves no manual Compose
editing through the drift API, validates serving ports, and fails
loudly per phase. The Makefile exposes the acceptance target and
lints the script.
"""

import os
import stat
import subprocess
import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent
SCRIPT = REPO / "scripts" / "acceptance.sh"


def read(rel: str) -> str:
    return (REPO / rel).read_text(encoding="utf-8")


class TestAcceptanceScript(unittest.TestCase):
    def test_script_exists_and_executable(self):
        self.assertTrue(SCRIPT.is_file(), "scripts/acceptance.sh missing")
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
        content = read("scripts/acceptance.sh")
        self.assertIn("set -euo pipefail", content)
        self.assertIn("id -u", content)

    def test_phases_cover_chain(self):
        content = read("scripts/acceptance.sh")
        for symbol in ("preconditions", "bootstrap", "claim", "admin",
                       "doctor", "firewall", "install_package",
                       "drift", "check_port", "acceptance PASSED"):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, content)

    def test_supported_surfaces_only(self):
        content = read("scripts/acceptance.sh")
        for symbol in ("apps plan", "apps install", "--follow",
                       "firewall apply", "/api/v1/catalog/",
                       "/drift", "no manual Compose editing"):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, content)
        for banned in ("docker compose exec", "docker exec", "vim ", "nano "):
            with self.subTest(banned=banned):
                self.assertNotIn(banned, content)

    def test_refuses_without_root(self):
        if os.geteuid() == 0:
            self.skipTest("running as root")
        env = {
            "PATH": os.environ["PATH"],
            "HOMESTEAD_APP_MOUNT": "/nonexistent-app",
            "HOMESTEAD_BULK_MOUNT": "/nonexistent-bulk",
            "HOMESTEAD_PASSWORD_FILE": "/nonexistent-pass",
        }
        proc = subprocess.run(
            ["bash", str(SCRIPT)],
            capture_output=True,
            text=True,
            timeout=30,
            env=env,
        )
        self.assertEqual(proc.returncode, 1)
        self.assertIn("run as root", proc.stderr)

    def test_makefile_targets(self):
        makefile = read("Makefile")
        self.assertRegex(makefile, r"(?m)^acceptance:")
        self.assertIn("bash -n scripts/acceptance.sh", makefile)


if __name__ == "__main__":
    unittest.main()
