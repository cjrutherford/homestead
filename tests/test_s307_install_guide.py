"""S307 — Install guide, bare metal to public apps.

docs/INSTALL.md is what an owner or fulfilment follows. These checks keep it
honest as the code moves: every script, doc path, make target and
homesteadctl command (with its flags) it names must exist, and it must
cover each part of the path.
"""

import re
import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent
GUIDE = REPO / "docs" / "INSTALL.md"
CTL = REPO / "cmd" / "homesteadctl"


def guide() -> str:
    return GUIDE.read_text(encoding="utf-8")


def ctl_source() -> str:
    return "\n".join(
        p.read_text(encoding="utf-8")
        for p in CTL.glob("*.go")
        if not p.name.endswith("_test.go")
    )


class TestInstallGuide(unittest.TestCase):
    def test_readme_links_guide(self):
        self.assertIn("docs/INSTALL.md", (REPO / "README.md").read_text(encoding="utf-8"))

    def test_named_paths_exist(self):
        text = guide()
        paths = set(re.findall(r"(?:scripts|docs)/[A-Za-z0-9_./-]+\.(?:sh|md)", text))
        self.assertTrue(paths)
        for rel in sorted(paths):
            with self.subTest(path=rel):
                self.assertTrue((REPO / rel).exists(), rel)

    def test_make_targets_exist(self):
        makefile = (REPO / "Makefile").read_text(encoding="utf-8")
        for target in ("install", "dashboard-install", "dashboard-build", "dashboard-embed"):
            with self.subTest(target=target):
                self.assertIn(f"make {target}" if target == "install" else target, guide())
                self.assertRegex(makefile, rf"(?m)^{re.escape(target)}:")

    def test_homesteadctl_commands_exist(self):
        run_go = (CTL / "run.go").read_text(encoding="utf-8")
        src = ctl_source()
        commands = re.findall(r"homesteadctl ((?:[a-z][a-z-]*)(?: [a-z][a-z-]*)?)((?: --[a-z-]+)*)", guide())
        self.assertTrue(commands)
        for words, flags in commands:
            top, *sub = words.split()
            with self.subTest(command=words):
                self.assertTrue(f'"{top}"' in run_go, top)
                for s in sub:
                    self.assertTrue(f'"{s}"' in src, s)
                for flag in re.findall(r"--([a-z-]+)", flags):
                    # flag.FlagSet names ("adopt") or hand-parsed ("--adopt")
                    self.assertTrue(f'"{flag}"' in src or f'"--{flag}"' in src, flag)

    def test_covers_each_part(self):
        text = guide()
        for needle in (
            "## Part A", "## Part B", "## Part C", "## Part D", "## Part E", "## Part F",
            "docker-compose-plugin", "cloudflared", "dashboard-embed", "bootstrap.sh",
            "claim token", "Account → Cloudflare Tunnel → Edit", "re-planned",
            "Review public address", "factory-reset.sh",
        ):
            with self.subTest(needle=needle):
                self.assertIn(needle, text)

    def test_sign_in_comes_before_domain(self):
        text = guide()
        self.assertLess(text.index("### D3. Sign-in"), text.index("### D4. Domain"))


if __name__ == "__main__":
    unittest.main()
