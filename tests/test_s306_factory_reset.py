"""S306 — Factory reset for preparing an appliance for a new owner.

scripts/factory-reset.sh returns an appliance to the unclaimed state. It is
root-gated, refuses to run without --yes, removes only what Homestead owns
(other Compose projects, volumes and images stay usable without Homestead),
and touches mounted storage only for paths named with --wipe-mount that are
mount points. It never formats or partitions.
"""

import os
import subprocess
import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent
SCRIPT = REPO / "scripts" / "factory-reset.sh"


def source() -> str:
    return SCRIPT.read_text(encoding="utf-8")


def run(*args: str) -> subprocess.CompletedProcess:
    return subprocess.run(
        ["bash", str(SCRIPT), *args], capture_output=True, text=True, timeout=30
    )


class TestFactoryResetScript(unittest.TestCase):
    def test_executable_and_valid_bash(self):
        self.assertTrue(os.access(SCRIPT, os.X_OK))
        subprocess.run(["bash", "-n", str(SCRIPT)], check=True)

    def test_linted_by_makefile(self):
        makefile = (REPO / "Makefile").read_text(encoding="utf-8")
        self.assertIn("bash -n scripts/factory-reset.sh", makefile)

    def test_help_and_bad_arguments(self):
        self.assertEqual(run("--help").returncode, 0)
        self.assertIn("--wipe-mount", run("--help").stdout)
        self.assertEqual(run("--bogus").returncode, 2)
        self.assertEqual(run("--wipe-mount").returncode, 2)

    @unittest.skipIf(os.geteuid() == 0, "root gate is only observable as non-root")
    def test_root_gated(self):
        result = run("--yes")
        self.assertEqual(result.returncode, 1)
        self.assertIn("run as root", result.stderr)

    def test_requires_confirmation(self):
        src = source()
        self.assertIn('if [ "$confirm" -ne 1 ]', src)
        # Confirmation is checked before the first destructive command.
        self.assertLess(src.index('"$confirm" -ne 1'), src.index("disable --now"))

    def test_removes_only_homestead_docker_objects(self):
        src = source()
        for forbidden in ("prune", "docker volume ls -q)", "docker ps -aq)", "rmi"):
            with self.subTest(forbidden=forbidden):
                self.assertNotIn(forbidden, src)
        self.assertIn('STACKS="/opt/homestead/stacks"', src)
        self.assertIn("label=com.docker.compose.project=$name", src)
        self.assertIn('EDGE_NETWORK="homestead-edge"', src)
        self.assertIn('EDGE_CONTAINER="homestead-caddy"', src)

    def test_names_match_the_code(self):
        networks = (REPO / "internal/docker/networks.go").read_text(encoding="utf-8")
        caddy = (REPO / "internal/docker/caddy.go").read_text(encoding="utf-8")
        self.assertIn('NetworkEdge = "homestead-edge"', networks)
        self.assertIn('CaddyContainerName = "homestead-caddy"', caddy)

    def test_removes_every_installed_unit_and_layout_dir(self):
        src = source()
        for unit in sorted((REPO / "systemd").glob("homestead-*.service")):
            with self.subTest(unit=unit.name):
                self.assertIn(unit.name, src)
        layout = (REPO / "scripts/provision-layout.sh").read_text(encoding="utf-8")
        for line in layout.splitlines():
            if line.startswith("provision /"):
                path = line.split()[1]
                top = "/opt/homestead" if path.startswith("/opt/homestead") else path
                with self.subTest(path=path):
                    self.assertIn(top, src)

    def test_mount_wipe_is_explicit_and_guarded(self):
        src = source()
        self.assertIn("mountpoint -q", src)
        self.assertIn("refusing to wipe system path", src)
        self.assertIn("! -name lost+found", src)
        for forbidden in ("mkfs", "parted", "fdisk", "wipefs"):
            with self.subTest(forbidden=forbidden):
                self.assertNotIn(forbidden, src)
        # Wipe targets are validated before anything is removed.
        self.assertLess(src.index("mountpoint -q"), src.index("disable --now"))

    def test_reinstall_uses_the_installer(self):
        self.assertIn('"$REPO/scripts/install-systemd.sh"', source())


if __name__ == "__main__":
    unittest.main()
