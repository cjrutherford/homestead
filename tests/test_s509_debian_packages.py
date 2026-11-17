"""S509: Debian packages include the native appliance and preserve its data."""

import os
import stat
import subprocess
import tempfile
import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


class DebianPackageTest(unittest.TestCase):
    def test_builder_creates_arch_specific_package_with_runtime_assets(self):
        if not shutil_which("dpkg-deb"):
            self.skipTest("dpkg-deb is not installed")
        with tempfile.TemporaryDirectory(prefix="homestead-deb-test-") as temp:
            root = Path(temp)
            release = root / "release"
            output = root / "output"
            release.mkdir()
            for name in ("homestead-server-linux-arm64", "homesteadctl-linux-arm64"):
                binary = release / name
                binary.write_bytes(b"arm64 fixture, packaging test only\n")
                binary.chmod(0o755)
            env = dict(os.environ, DEB_MAINTAINER="Package Test <packages@example.invalid>")
            subprocess.run(
                [
                    str(REPO / "scripts/build-deb.sh"),
                    "arm64",
                    str(release),
                    str(output),
                ],
                cwd=REPO,
                env=env,
                check=True,
                capture_output=True,
                text=True,
            )
            package = output / "homestead_0.0.1-1_arm64.deb"
            metadata = subprocess.run(
                ["dpkg-deb", "-f", str(package)], check=True, capture_output=True, text=True
            ).stdout
            self.assertIn("Package: homestead\n", metadata)
            self.assertIn("Version: 0.0.1-1\n", metadata)
            self.assertIn("Architecture: arm64\n", metadata)
            self.assertIn("Maintainer: Package Test <packages@example.invalid>\n", metadata)
            contents = subprocess.run(
                ["dpkg-deb", "-c", str(package)], check=True, capture_output=True, text=True
            ).stdout
            for path in (
                "usr/local/bin/homestead-server",
                "usr/local/bin/homesteadctl",
                "usr/local/share/homestead/catalog/mealie.yaml",
                "usr/local/share/homestead/catalog/craft/context/",
                "usr/local/share/homestead/catalog/everything/context/",
                "usr/local/share/homestead/catalog/everything/sources/",
                "usr/lib/systemd/system/homestead-server.service",
                "usr/lib/systemd/system/homestead-apply.service",
                "usr/lib/systemd/system/homestead-tunnel.service",
                "usr/share/doc/homestead/README.Debian",
            ):
                self.assertIn(path, contents)
            self.assertNotIn("node_modules", contents)
            self.assertNotIn("secret.key", contents)

    def test_maintainer_scripts_provision_without_erasing_appliance_data(self):
        scripts = REPO / "packaging/debian"
        for name in ("postinst", "prerm", "postrm"):
            subprocess.run(["sh", "-n", str(scripts / name)], check=True)

        with tempfile.TemporaryDirectory(prefix="homestead-deb-lifecycle-") as temp:
            root = Path(temp) / "root"
            mocks = Path(temp) / "bin"
            log = Path(temp) / "commands.log"
            mocks.mkdir()
            root.joinpath("var/lib/homestead").mkdir(parents=True, mode=0o700)
            root.joinpath("etc/homestead").mkdir(parents=True)
            config = root / "etc/homestead/desired.yaml"
            config.write_text("existing config", encoding="utf-8")
            sentinel = root / "var/lib/homestead/keep.db"
            sentinel.write_text("existing state", encoding="utf-8")

            def mock(name: str, body: str) -> None:
                path = mocks / name
                path.write_text("#!/bin/sh\n" + body + "\n", encoding="utf-8")
                path.chmod(path.stat().st_mode | stat.S_IXUSR | stat.S_IXGRP | stat.S_IXOTH)

            mock("getent", 'exit 1')
            mock("groupadd", f'printf "groupadd %s\\n" "$*" >> "{log}"')
            for name in ("systemctl", "deb-systemd-helper", "deb-systemd-invoke"):
                mock(name, f'printf "{name} %s\\n" "$*" >> "{log}"')
            env = dict(os.environ, HOMESTEAD_TEST_ROOT=str(root), PATH=f"{mocks}:{os.environ['PATH']}")

            subprocess.run(["sh", str(scripts / "postinst"), "configure"], env=env, check=True)
            self.assertTrue((root / "etc/homestead").is_dir())
            self.assertEqual(stat.S_IMODE((root / "var/lib/homestead").stat().st_mode), 0o700)
            self.assertEqual(sentinel.read_text(encoding="utf-8"), "existing state")
            self.assertEqual(config.read_text(encoding="utf-8"), "existing config")

            with log.open("a", encoding="utf-8") as f:
                f.write("---upgrade---\n")
            subprocess.run(
                ["sh", str(scripts / "postinst"), "configure", "0.0.1-1"], env=env, check=True
            )
            subprocess.run(["sh", str(scripts / "prerm"), "remove"], env=env, check=True)
            subprocess.run(["sh", str(scripts / "postrm"), "purge"], env=env, check=True)

            calls = log.read_text(encoding="utf-8")
            self.assertIn("deb-systemd-invoke start homestead-server.service", calls)
            self.assertIn("deb-systemd-invoke try-restart homestead-server.service", calls)
            self.assertIn("deb-systemd-invoke stop homestead-server.service", calls)
            self.assertIn("deb-systemd-helper disable homestead-server.service", calls)
            self.assertIn("deb-systemd-helper purge homestead-server.service", calls)
            self.assertNotIn("docker", calls.lower())
            self.assertNotIn("restart docker.service", calls)
            self.assertNotIn("rm -rf", calls)
            self.assertEqual(sentinel.read_text(encoding="utf-8"), "existing state")
            self.assertEqual(config.read_text(encoding="utf-8"), "existing config")


def shutil_which(name: str) -> str | None:
    from shutil import which

    return which(name)


if __name__ == "__main__":
    unittest.main()
