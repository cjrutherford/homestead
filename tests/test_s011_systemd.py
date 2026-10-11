"""S011 — systemd Installation conformance tests.

Static guardrails for the native service installation: the unit file is a
structurally valid systemd unit with the production binary path, loopback
bind, restart policy, and hardening baseline; the install/uninstall scripts
are present, executable, syntactically valid, root-gated, and idempotent by
construction; the Makefile exposes the lifecycle targets.
"""

import configparser
import subprocess
import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent
UNIT = REPO / "systemd" / "homestead-server.service"


def read(rel: str) -> str:
    return (REPO / rel).read_text(encoding="utf-8")


def load_unit() -> configparser.ConfigParser:
    parser = configparser.ConfigParser(strict=True)
    parser.read(UNIT)
    return parser


class TestUnitFile(unittest.TestCase):
    def test_unit_file_parses_as_ini(self):
        parser = load_unit()
        for section in ("Unit", "Service", "Install"):
            with self.subTest(section=section):
                self.assertIn(section, parser.sections())

    def test_exec_start_is_production_binary_loopback(self):
        exec_start = load_unit()["Service"]["ExecStart"]
        self.assertTrue(
            exec_start.startswith("/usr/local/bin/homestead-server"),
            f"ExecStart must use the ARCHITECTURE.md path, got: {exec_start}",
        )
        self.assertIn("--listen 127.0.0.1:", exec_start)

    def test_exec_start_pins_packaged_catalog(self):
        exec_start = load_unit()["Service"]["ExecStart"]
        self.assertIn("--catalog-dir /usr/local/share/homestead/catalog", exec_start)

    def test_writable_paths_cover_state_and_stacks(self):
        paths = load_unit()["Service"]["ReadWritePaths"]
        for want in ("/etc/homestead", "/var/lib/homestead",
                     "/opt/homestead/stacks", "/mnt", "/media"):
            with self.subTest(want=want):
                self.assertIn(want, paths)

    def test_restart_policy(self):
        service = load_unit()["Service"]
        self.assertEqual(service["Restart"], "on-failure")
        self.assertIn("RestartSec", service)

    def test_runs_root_owned(self):
        """ADR-001: native daemon is a root-owned systemd service."""
        self.assertEqual(load_unit()["Service"]["User"], "root")

    def test_wanted_by_multi_user(self):
        self.assertEqual(
            load_unit()["Install"]["WantedBy"], "multi-user.target"
        )

    def test_hardening_baseline(self):
        service = load_unit()["Service"]
        for key in ("NoNewPrivileges", "ProtectSystem", "ProtectHome", "PrivateTmp"):
            with self.subTest(key=key):
                self.assertIn(key, service)


class TestInstallScripts(unittest.TestCase):
    SCRIPTS = ("scripts/install-systemd.sh", "scripts/uninstall-systemd.sh")

    def test_scripts_exist_and_executable(self):
        for script in self.SCRIPTS:
            with self.subTest(script=script):
                path = REPO / script
                self.assertTrue(path.is_file())
                self.assertTrue(path.stat().st_mode & 0o111)

    def test_scripts_pass_bash_syntax_check(self):
        for script in self.SCRIPTS:
            with self.subTest(script=script):
                proc = subprocess.run(
                    ["bash", "-n", str(REPO / script)],
                    capture_output=True,
                    text=True,
                    timeout=30,
                )
                self.assertEqual(proc.returncode, 0, proc.stderr)

    def test_scripts_require_root_and_strict_mode(self):
        for script in self.SCRIPTS:
            with self.subTest(script=script):
                content = read(script)
                self.assertIn("set -euo pipefail", content)
                self.assertIn('id -u', content)

    def test_install_reloads_and_enables(self):
        content = read("scripts/install-systemd.sh")
        for step in ("daemon-reload", "systemctl enable", "systemctl restart"):
            with self.subTest(step=step):
                self.assertIn(step, content)

    def test_install_embeds_built_dashboard(self):
        content = read("scripts/install-systemd.sh")
        for symbol in ("internal/dashboard/dist/index.html", "-tags embeddash"):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, content)

    def test_install_places_packaged_catalog(self):
        content = read("scripts/install-systemd.sh")
        for symbol in ("catalog", "/usr/local/share/homestead/catalog"):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, content)

    def test_uninstall_disables_and_cleans_up(self):
        content = read("scripts/uninstall-systemd.sh")
        self.assertIn("disable --now", content)
        self.assertIn("/etc/systemd/system/homestead-server.service", content)
        self.assertIn("/usr/local/bin/homestead-server", content)


class TestLifecycleTargets(unittest.TestCase):
    def test_makefile_targets_exist(self):
        makefile = read("Makefile")
        for target in ("install", "uninstall", "lint-scripts"):
            with self.subTest(target=target):
                self.assertRegex(makefile, rf"(?m)^{target}:")


if __name__ == "__main__":
    unittest.main()
