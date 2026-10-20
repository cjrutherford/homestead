"""S012 — Filesystem Layout conformance tests.

Guardrails for the daemon-managed layout: the Go definition and the
provision script agree on the exact directory set from docs/ARCHITECTURE.md,
the private state directory stays private, the unit permits exactly the
layout it provisions, install provisions before starting, and uninstall
never touches customer state.
"""

import configparser
import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent

EXPECTED_DIRS = (
    "/etc/homestead",
    "/var/lib/homestead",
    "/var/log/homestead",
    "/run/homestead",
    "/opt/homestead/stacks",
)


def read(rel: str) -> str:
    return (REPO / rel).read_text(encoding="utf-8")


class TestLayoutDefinition(unittest.TestCase):
    def test_go_package_defines_full_set(self):
        src = read("internal/layout/layout.go")
        for path in EXPECTED_DIRS:
            with self.subTest(path=path):
                self.assertIn(f'"{path}"', src)

    def test_provision_script_matches_go_package(self):
        """Shell and Go must provision the same set — no drift either way."""
        script = read("scripts/provision-layout.sh")
        for path in EXPECTED_DIRS:
            with self.subTest(path=path):
                self.assertIn(path, script)

    def test_lib_dir_is_private_in_both(self):
        self.assertIn("0o700", read("internal/layout/layout.go"))
        self.assertIn("/var/lib/homestead 0700", read("scripts/provision-layout.sh"))

    def test_provision_is_root_gated_and_idempotent(self):
        script = read("scripts/provision-layout.sh")
        self.assertIn("set -euo pipefail", script)
        self.assertIn("id -u", script)
        self.assertIn("install -d -o root -g root", script)


class TestUnitWiring(unittest.TestCase):
    def test_runtime_directory_managed(self):
        service = configparser.ConfigParser(strict=True)
        service.read(REPO / "systemd" / "homestead-server.service")
        self.assertEqual(service["Service"]["RuntimeDirectory"], "homestead homestead-craft homestead-dashboard")
        self.assertEqual(service["Service"]["RuntimeDirectoryPreserve"], "yes")

    def test_read_write_paths_cover_state_dirs(self):
        paths = configparser.ConfigParser(strict=True)
        paths.read(REPO / "systemd" / "homestead-server.service")
        granted = paths["Service"]["ReadWritePaths"].split()
        for path in ("/etc/homestead", "/var/lib/homestead", "/var/log/homestead"):
            with self.subTest(path=path):
                self.assertIn(path, granted)

    def test_install_provisions_before_starting(self):
        content = read("scripts/install-systemd.sh")
        self.assertIn("provision-layout.sh", content)
        self.assertLess(
            content.index("provision-layout.sh"),
            content.index("systemctl restart"),
        )


class TestUninstallPreservesState(unittest.TestCase):
    def test_uninstall_touches_no_state_dir(self):
        """Uninstall removes service + binary only; customer state survives."""
        content = read("scripts/uninstall-systemd.sh")
        for path in EXPECTED_DIRS:
            with self.subTest(path=path):
                self.assertNotIn(path, content)


if __name__ == "__main__":
    unittest.main()
