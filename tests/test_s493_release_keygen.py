"""S493 - release signing key generation and rotation seam."""

import os
import shutil
from _tools import require_tool
import stat
import subprocess
import tempfile
import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent
SCRIPT = REPO / "scripts/release/keygen.sh"
PUB_LINE = "RWQ+22aq9bM9sFj9nQ6kvyg5+UZnmMyNJM9k1QE8Bv0Bd7MaYzfLS+NQ"
KEY_ID = "B03DB3F5AA66DB3E"

STUB = """#!/usr/bin/env bash
while [ $# -gt 0 ]; do case "$1" in -s) s="$2"; shift 2;; -p) p="$2"; shift 2;; -c) c="$2"; shift 2;; *) shift;; esac; done
echo SECRET > "$s"
printf 'untrusted comment: %s\\n{line}\\n' "$c" > "$p"
""".replace("{line}", PUB_LINE)


def read(rel):
    return (REPO / rel).read_text(encoding="utf-8")


def run(args, path_prefix=None):
    env = dict(os.environ)
    if path_prefix:
        env["PATH"] = f"{path_prefix}:{env['PATH']}"
    return subprocess.run([str(SCRIPT), *args], capture_output=True, text=True, env=env)


class TestKeygen(unittest.TestCase):
    def setUp(self):
        self.tmp = Path(tempfile.mkdtemp())
        self.addCleanup(shutil.rmtree, self.tmp, True)
        self.bin = self.tmp / "bin"
        self.bin.mkdir()
        stub = self.bin / "minisign"
        stub.write_text(STUB)
        stub.chmod(0o755)
        self.keys = self.tmp / "keys"
        self.keys.mkdir()

    def test_generates_outside_repo_mode_600_prints_public_only(self):
        out = self.keys / "release.key"
        r = run(["--out", str(out)], self.bin)
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertEqual(stat.S_IMODE(out.stat().st_mode), 0o600)
        self.assertIn(PUB_LINE, r.stdout)
        self.assertIn(KEY_ID, r.stdout)
        self.assertNotIn("SECRET", r.stdout + r.stderr)

    def test_refuses_path_inside_repo(self):
        r = run(["--out", str(REPO / "release-s493-test.key")], self.bin)
        self.assertEqual(r.returncode, 1)
        self.assertIn("inside the repository", r.stderr)
        self.assertFalse((REPO / "release-s493-test.key").exists())

    def test_refuses_symlinked_dir_into_repo(self):
        link = self.tmp / "link"
        link.symlink_to(REPO)
        r = run(["--out", str(link / "release-s493-test.key")], self.bin)
        self.assertEqual(r.returncode, 1)
        self.assertFalse((REPO / "release-s493-test.key").exists())

    def test_refuses_overwrite_and_missing_dir(self):
        out = self.keys / "release.key"
        out.write_text("x")
        self.assertEqual(run(["--out", str(out)], self.bin).returncode, 1)
        self.assertEqual(run(["--out", str(self.tmp / "nope" / "k")], self.bin).returncode, 2)
        self.assertEqual(run([], self.bin).returncode, 2)

    @require_tool("minisign")
    def test_real_minisign(self):
        out = self.keys / "real.key"
        r = run(["--out", str(out), "--insecure-no-passphrase"])
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertEqual(stat.S_IMODE(out.stat().st_mode), 0o600)
        self.assertRegex(r.stdout, r"key ID:\s+[0-9A-F]{16}")


class TestSeamAndDocs(unittest.TestCase):
    def test_build_time_seam(self):
        self.assertIn("ReleasePublicKey", read("internal/release/store.go"))
        mk = read("Makefile")
        self.assertIn("RELEASE_PUBLIC_KEY_FILE", mk)
        self.assertIn("homestead/internal/release.ReleasePublicKey", mk)
        self.assertIn("TestBuildTimeReleaseKeyReplacesPlaceholder", read("internal/release/store_test.go"))
        self.assertIn("TestRotationEndToEnd", read("internal/release/store_test.go"))

    def test_script_never_prints_private_key(self):
        sh = SCRIPT.read_text()
        self.assertNotIn('cat "$real"', sh)
        self.assertIn("refusing to write a key inside the repository", sh)

    def test_docs(self):
        rel = read("docs/RELEASING.md")
        self.assertIn("## Release signing key", rel)
        self.assertIn("keygen.sh", rel)
        self.assertIn("homesteadctl updates trust", rel)
        self.assertIn("RELEASE_PUBLIC_KEY_FILE", rel)
        self.assertIn("S493", read("docs/SLICE_INDEX.md"))
        self.assertIn("PLAT-UPDATE-4", read("docs/reviews/OWNER_ACCEPTANCE_CHECKLIST.md"))
        self.assertTrue((REPO / "docs/slices/S493-release-key-generation-and-rotation.md").exists())


if __name__ == "__main__":
    unittest.main()
