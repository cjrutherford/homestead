"""S422 - Uptime Kuma single sign-in (ADR-055 second section 6 amendment).

Kuma's own login is off behind the admin-only gate and the host block. The Go
tests run the script; this pins the manifest facts and the docs.
"""
import re
import unittest
from pathlib import Path

import yaml

REPO = Path(__file__).resolve().parent.parent


def read(p):
    return (REPO / p).read_text()


class TestKumaSingleSignIn(unittest.TestCase):
    def setUp(self):
        self.m = yaml.safe_load(read("catalog/uptime-kuma.yaml"))
        self.fa = self.m["sso"]["forwardauth"]
        self.o = self.fa["ownloginoff"]

    def test_requires_the_host_block_and_admin_only(self):
        self.assertTrue(self.fa["requireshostblock"])
        self.assertEqual(self.fa["access"], "admin-only")
        self.assertNotIn("env", self.fa)

    def test_commands_share_one_pinned_script(self):
        self.assertEqual(self.m["version"], "2.5.7")
        script = self.o["prepare"][2]
        self.assertEqual(script, self.o["onapply"][2])
        self.assertEqual(script, self.o["onoff"][2])
        self.assertIn("const VERIFIED = '2.5.7'", script)
        for key, mode in (("prepare", "prepare"), ("onapply", "on"), ("onoff", "off")):
            self.assertEqual(self.o[key][:2], ["node", "-e"])
            self.assertEqual(self.o[key][3], mode)
            self.assertEqual(len(self.o[key]), 4)

    def test_script_guards_and_keeps_secret_off_argv(self):
        script = self.o["onapply"][2]
        for needle in ("pragma_table_info", "BEGIN IMMEDIATE", "SELECT COUNT(*) AS n FROM user",
                       "fs.readFileSync(0", "disableAuth", "'general'", "ROLLBACK"):
            self.assertIn(needle, script)
        self.assertIsNone(re.search(r"process\.argv\[[2-9]\]", script))
        for bad in ("DELETE", "DROP", "execSync", "spawn", "console.log(who"):
            self.assertNotIn(bad, script)

    def test_login_still_stays_off_the_published_ports(self):
        self.assertFalse(self.m["requirements"].get("ports"))
        self.assertFalse(self.m["exposure"]["endpoints"][0]["allowwider"])

    def test_bypass_list_unchanged_and_never_socket_or_metrics(self):
        paths = [b["path"] for b in self.fa["bypass"]]
        self.assertEqual(len(paths), 7)
        for never in ("/metrics", "/socket.io/", "/dashboard", "/"):
            for p in paths:
                self.assertIsNone(re.match(p, never), (p, never))

    def test_no_secret_code_path(self):
        src = read("internal/appsso/ownlogin.go")
        self.assertIn("bcrypt.GenerateFromPassword", src)
        self.assertIn("execIn(ctx, container, user, stdin, argv...)", src)

    def test_off_runs_login_on_before_the_gate_goes(self):
        src = read("internal/appsso/forwardauth.go")
        off = src.split("reach,\n\t\t\tloginOn,")[1].split("ungate")[0]
        self.assertIn("unwire", off)
        on = src.split('{Name: "gate"')[1].split('{Name: "record"')[0]
        self.assertIn("loginOff", on)

    def test_docs(self):
        adr = read("docs/adr/ADR-055-forward-auth-for-navidrome-and-uptime-kuma.md")
        self.assertIn("Amendment, 2026-11-01 (S422", adr)
        self.assertEqual(adr.split("## Status")[1].splitlines()[1], "Accepted")
        slice_doc = read("docs/slices/S422-uptime-kuma-single-sign-in.md")
        for h in ("Goal", "Dependencies", "Scope", "Acceptance", "Tests", "Built", "Verify live"):
            self.assertIn("## " + h + "\n", slice_doc)
        self.assertIn("npm run reset-password", slice_doc)
        for doc in ("docs/DEFERRED.md", "docs/SLICE_INDEX.md"):
            self.assertIn("S422", read(doc))


if __name__ == "__main__":
    unittest.main()
