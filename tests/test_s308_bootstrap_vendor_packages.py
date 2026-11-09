"""S308 — Bootstrap installs Docker and cloudflared (ADR-042).

bootstrap.sh adds Docker's and Cloudflare's apt repositories only after the
downloaded key matches a pinned fingerprint, keeps what is already
installed, never enables a cloudflared service of its own (homestead-tunnel
runs the connector after an approved plan), and finishes both before the
systemd install so the daemon can start the edge.
"""

import re
import subprocess
import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent
SCRIPT = REPO / "scripts" / "bootstrap.sh"
ADR = REPO / "docs" / "adr" / "ADR-042-bootstrap-installs-docker-and-cloudflared.md"

DOCKER_FPR = "9DC858229FC7DD38854AE2D88D81803C0EBFCD88"
CLOUDFLARE_FPR = "CC94B39C77AE7342A68B89628A682D308D4E5E73"


def source() -> str:
    return SCRIPT.read_text(encoding="utf-8")


class TestBootstrapVendorPackages(unittest.TestCase):
    def test_fingerprints_pinned_in_script_and_adr(self):
        src, adr = source(), ADR.read_text(encoding="utf-8")
        for fpr in (DOCKER_FPR, CLOUDFLARE_FPR):
            with self.subTest(fpr=fpr):
                self.assertIn(fpr, src)
                self.assertIn(fpr, adr)
        self.assertIn("HOMESTEAD_DOCKER_KEY_FPR", src)
        self.assertIn("HOMESTEAD_CLOUDFLARE_KEY_FPR", src)

    def test_keys_checked_before_repositories_are_added(self):
        src = source()
        self.assertIn("gpg --show-keys --with-colons", src)
        self.assertIn("refusing to trust it", src)
        self.assertLess(src.index('fetch_key "https://download.docker.com'), src.index("sources.list.d/docker.list"))
        self.assertLess(src.index("fetch_key https://pkg.cloudflare.com"), src.index("sources.list.d/cloudflared.list"))
        self.assertIn("signed-by=/etc/apt/keyrings/docker.asc", src)
        self.assertIn("signed-by=/usr/share/keyrings/cloudflare-public-v2.gpg", src)

    def test_installs_engine_with_compose_and_starts_it(self):
        src = source()
        for pkg in ("docker-ce", "docker-ce-cli", "containerd.io", "docker-compose-plugin", "cloudflared"):
            with self.subTest(pkg=pkg):
                self.assertRegex(src, rf"apt-get install -y [^\n]*\b{re.escape(pkg)}\b")
        self.assertIn("systemctl enable --now docker.service", src)
        self.assertIn("docker compose version", src)

    def test_never_runs_its_own_connector(self):
        src = source()
        self.assertNotIn("cloudflared service install", src)
        self.assertNotIn("tunnel login", src)
        self.assertNotRegex(src, r"systemctl [^\n]*cloudflared")

    def test_keeps_existing_installs(self):
        src = source()
        self.assertIn("docker present:", src)
        self.assertIn("cloudflared present:", src)
        self.assertIn("installed without Compose v2", src)

    def test_vendor_installs_come_before_systemd_install(self):
        src = source()
        last = src.rindex('"$REPO/scripts/install-systemd.sh"')
        self.assertLess(src.index("docker-compose-plugin"), last)
        self.assertLess(src.index("apt-get install -y cloudflared"), last)

    def test_opt_outs(self):
        src = source()
        self.assertIn("--no-docker) WANT_DOCKER=0", src)
        self.assertIn("--no-cloudflared) WANT_CLOUDFLARED=0", src)
        result = subprocess.run(["bash", str(SCRIPT), "--bogus"], capture_output=True, text=True, timeout=30)
        self.assertEqual(result.returncode, 2)
        self.assertIn("unknown argument", result.stderr)


if __name__ == "__main__":
    unittest.main()
