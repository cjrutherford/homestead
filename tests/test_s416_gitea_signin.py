"""S416 — Gitea sign-in script behaviour.

The manifest's inline sh script writes Gitea's own login_source table
through the sqlite3 shell. This runs it against a temporary database with
the 28.2.0 table layout (verified from a live gitea/gitea:28.2.0 image).
Without a sqlite3 shell on the host, a tiny stand-in that supports the
shell features the script uses (-cmd/-bail, readfile(), a statement as an
argument or a script on stdin) is put on PATH.
"""
import json
import os
import shutil
import sqlite3
import subprocess
import tempfile
import unittest
from pathlib import Path

import yaml

REPO = Path(__file__).resolve().parent.parent
SCHEMA = ("CREATE TABLE `login_source` (`id` INTEGER PRIMARY KEY AUTOINCREMENT NOT NULL, `type` INTEGER NULL, "
          "`name` TEXT NULL, `is_active` INTEGER DEFAULT 0 NOT NULL, `is_sync_enabled` INTEGER DEFAULT 0 NOT NULL, "
          "`two_factor_policy` TEXT DEFAULT '' NOT NULL, `cfg` TEXT NULL, `created_unix` INTEGER NULL, "
          "`updated_unix` INTEGER NULL); CREATE UNIQUE INDEX `UQE_login_source_name` ON `login_source` (`name`);")

SHIM = r'''#!/usr/bin/env python3
import sqlite3, sys
a = sys.argv[1:]
db = None
args = []
i = 0
while i < len(a):
    if a[i] == "-cmd": i += 2; continue
    if a[i] == "-bail": i += 1; continue
    args.append(a[i]); i += 1
def readfile(p):
    try:
        return open(p, "rb").read().decode()
    except OSError:
        return None
c = sqlite3.connect(args[0], timeout=30, isolation_level=None)
c.create_function("readfile", 1, readfile)
try:
    if len(args) > 1:
        for r in c.execute(args[1]):
            print("|".join("" if v is None else str(v) for v in r))
    else:
        c.executescript(sys.stdin.read())
except sqlite3.Error as e:
    sys.exit("Error: %s" % e)
'''


class TestGiteaSignInScript(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        m = yaml.safe_load((REPO / "catalog" / "gitea.yaml").read_text())
        env = m["sso"]["oidc"]["env"]
        cls.on, cls.off = env["onapply"], env["onoff"]
        cls.script = cls.on[2]
        assert cls.off[2] == cls.script and cls.on[-1] == "on" and cls.off[-1] == "off"
        assert cls.on[:2] == ["sh", "-c"] and cls.on[3] == "sh"

    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.dir = Path(self.tmp.name)
        self.addCleanup(self.tmp.cleanup)
        bin_ = self.dir / "bin"
        bin_.mkdir()
        if not shutil.which("sqlite3"):
            (bin_ / "sqlite3").write_text(SHIM)
            (bin_ / "sqlite3").chmod(0o755)
        (bin_ / "gitea").write_text("#!/bin/sh\necho 'gitea version 28.2.0 built with go1.27.2 : bindata'\n")
        (bin_ / "gitea").chmod(0o755)
        (self.dir / "sf.json").write_text(json.dumps({
            "client_id": "homestead-gitea", "client_secret": "sek-ret", "admin_group": "g-admins",
            "metadata_url": "https://a/.well-known/openid-configuration"}))
        self.make_db()

    def make_db(self, schema=SCHEMA):
        p = self.dir / "gitea.db"
        if p.exists():
            p.unlink()
        db = sqlite3.connect(p)
        db.executescript(schema)
        db.execute("insert into login_source(type,name,is_active,cfg) values(1,'local',1,'{}')")
        db.commit()
        db.close()

    def run_script(self, mode, **extra):
        src = self.script.replace("/data/gitea/gitea.db", str(self.dir / "gitea.db"))
        env = dict(os.environ, PATH=str(self.dir / "bin") + os.pathsep + os.environ["PATH"],
                   GITEA_SIGNIN_FILE=str(self.dir / "sf.json"), **extra)
        return subprocess.run(["sh", "-c", src, "sh", mode], env=env, capture_output=True, text=True)

    def rows(self, name="authentik"):
        db = sqlite3.connect(self.dir / "gitea.db")
        db.row_factory = sqlite3.Row
        r = db.execute("select * from login_source where name=?", (name,)).fetchall()
        n = db.execute("select count(*) from login_source").fetchone()[0]
        db.close()
        return [dict(x) for x in r], n

    def test_on_inserts_the_named_source_once(self):
        for _ in range(2):  # idempotent
            r = self.run_script("on")
            self.assertEqual(r.returncode, 0, r.stderr)
        rows, n = self.rows()
        self.assertEqual((len(rows), n), (1, 2), "one authentik row, the other row untouched")
        row = rows[0]
        self.assertEqual((row["type"], row["is_active"], row["is_sync_enabled"], row["two_factor_policy"]), (6, 1, 0, ""))
        self.assertGreater(row["created_unix"], 0)
        cfg = json.loads(row["cfg"])
        self.assertEqual((cfg["Provider"], cfg["ClientID"], cfg["ClientSecret"], cfg["AdminGroup"], cfg["GroupClaimName"]),
                         ("openidConnect", "homestead-gitea", "sek-ret", "g-admins", "groups"))
        self.assertEqual(cfg["Scopes"], ["openid", "profile", "email"])
        self.assertEqual(cfg["OpenIDConnectAutoDiscoveryURL"], "https://a/.well-known/openid-configuration")
        self.assertIs(cfg["GroupTeamMapRemoval"], False)
        self.assertIsNone(cfg["CustomURLMapping"])
        self.assertEqual(self.rows("local")[0][0]["cfg"], "{}")
        self.assertNotIn("sek-ret", r.stdout + r.stderr)

    def test_on_updates_an_existing_homestead_row_and_keeps_its_id(self):
        self.run_script("on")
        first = self.rows()[0][0]["id"]
        (self.dir / "sf.json").write_text(json.dumps({
            "client_id": "homestead-gitea", "client_secret": "second", "admin_group": "g2",
            "metadata_url": "https://b/.well-known/openid-configuration"}))
        self.assertEqual(self.run_script("on").returncode, 0)
        row = self.rows()[0][0]
        self.assertEqual(row["id"], first)
        self.assertEqual(json.loads(row["cfg"])["ClientSecret"], "second")

    def test_off_deactivates_and_clears_the_secret(self):
        self.run_script("on")
        r = self.run_script("off")
        self.assertEqual(r.returncode, 0, r.stderr)
        row = self.rows()[0][0]
        cfg = json.loads(row["cfg"])
        self.assertEqual((row["is_active"], cfg["ClientSecret"], cfg["ClientID"]), (0, "", "homestead-gitea"))
        self.assertEqual(self.run_script("on").returncode, 0, "on after off reuses the row")
        self.assertEqual((self.rows()[0][0]["is_active"], self.rows()[1]), (1, 2))
        # off with no row is a no-op
        self.make_db()
        self.assertEqual(self.run_script("off").returncode, 0)
        self.assertEqual(self.rows()[1], 1)

    def test_foreign_same_name_source_is_refused(self):
        db = sqlite3.connect(self.dir / "gitea.db")
        db.execute("insert into login_source(type,name,is_active,cfg) values(6,'authentik',1,?)",
                   (json.dumps({"ClientID": "someone-elses", "ClientSecret": "theirs"}),))
        db.commit()
        db.close()
        r = self.run_script("on")
        self.assertNotEqual(r.returncode, 0)
        self.assertIn("did not create", r.stderr)
        self.assertEqual(self.run_script("off").returncode, 0, "off leaves it alone")
        row = self.rows()[0][0]
        self.assertEqual((row["is_active"], json.loads(row["cfg"])["ClientSecret"]), (1, "theirs"))

    def test_wrong_version_is_refused_without_writing(self):
        (self.dir / "bin" / "gitea").write_text("#!/bin/sh\necho 'gitea version 29.0.0 built with go1.28'\n")
        r = self.run_script("on")
        self.assertNotEqual(r.returncode, 0)
        self.assertIn("verified against 28.2.0", r.stderr)
        self.assertIn("re-verify", r.stderr)
        self.assertEqual(self.rows()[0], [])

    def test_missing_column_fails_loudly_in_both_directions(self):
        self.make_db(SCHEMA.replace("`two_factor_policy` TEXT DEFAULT '' NOT NULL, ", ""))
        for mode in ("on", "off"):
            r = self.run_script(mode)
            self.assertNotEqual(r.returncode, 0)
            self.assertIn("lacks expected columns", r.stderr)
            self.assertIn("re-verify", r.stderr)

    def test_missing_database_bad_mode_and_empty_values(self):
        (self.dir / "sf.json").write_text("{}")
        r = self.run_script("on")
        self.assertNotEqual(r.returncode, 0)
        self.assertIn("missing or empty", r.stderr)
        self.assertEqual(self.rows()[0], [])
        self.assertIn("on or off", self.run_script("sideways").stderr)
        (self.dir / "gitea.db").unlink()
        self.assertIn("does not exist", self.run_script("on").stderr)


if __name__ == "__main__":
    unittest.main()
