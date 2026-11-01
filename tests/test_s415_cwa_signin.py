"""S415 — Calibre-Web Automated sign-in script behaviour.

The manifest's inline script edits CWA's own SQLite settings database. This
runs it against a temporary database with the 4.0.8 table layout.
"""
import json
import os
import sqlite3
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

import yaml

REPO = Path(__file__).resolve().parent.parent
OAUTH = ("id,provider_name,oauth_client_id,oauth_client_secret,oauth_base_url,oauth_authorize_url,"
         "oauth_token_url,oauth_userinfo_url,oauth_admin_group,metadata_url,scope,username_mapper,"
         "email_mapper,login_button,active")


class TestCwaSignInScript(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        m = yaml.safe_load((REPO / "catalog" / "calibre-web-automated.yaml").read_text())
        env = m["sso"]["oidc"]["env"]
        cls.on, cls.off = env["onapply"], env["onoff"]
        cls.script = cls.on[2]
        assert cls.off[2] == cls.script and cls.on[-1] == "on" and cls.off[-1] == "off"

    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.dir = Path(self.tmp.name)
        self.addCleanup(self.tmp.cleanup)
        (self.dir / "REL").write_text("v4.0.8\n")
        (self.dir / "sf.json").write_text(json.dumps({
            "client_id": "cid", "client_secret": "sek", "admin_group": "g-admins",
            "metadata_url": "https://a/.well-known/openid-configuration"}))
        self.make_db()

    def make_db(self, oauth=OAUTH):
        db = sqlite3.connect(self.dir / "app.db")
        db.execute("create table oauthProvider(%s)" % oauth)
        for i, n in enumerate(("github", "google", "generic")):
            db.execute("insert into oauthProvider(id,provider_name) values(?,?)", (i + 1, n))
        db.execute("create table settings(id,config_login_type,config_oauth_redirect_host,"
                   "config_enable_oauth_group_admin_management,config_disable_standard_login,config_default_role)")
        db.execute("insert into settings values(1,0,'',0,0,1)")
        db.commit()
        db.close()

    def run_script(self, mode, url="https://cwa.lan/"):
        src = (self.script.replace('"/config/app.db"', repr(str(self.dir / "app.db")))
               .replace('"/app/CWA_RELEASE"', repr(str(self.dir / "REL"))))
        env = dict(os.environ, CWA_SIGNIN_FILE=str(self.dir / "sf.json"), HOMESTEAD_CWA_URL=url)
        return subprocess.run([sys.executable, "-I", "-c", src, mode], env=env, capture_output=True, text=True)

    def rows(self):
        db = sqlite3.connect(self.dir / "app.db")
        db.row_factory = sqlite3.Row
        g = dict(db.execute("select * from oauthProvider where provider_name='generic'").fetchone())
        s = dict(db.execute("select * from settings").fetchone())
        others = db.execute("select count(*) from oauthProvider where active is not null").fetchone()[0]
        db.close()
        return g, s, others

    def test_on_updates_the_generic_row_and_settings(self):
        r = self.run_script("on")
        self.assertEqual(r.returncode, 0, r.stderr)
        g, s, others = self.rows()
        self.assertEqual((g["oauth_client_id"], g["oauth_client_secret"], g["oauth_admin_group"], g["active"]),
                         ("cid", "sek", "g-admins", 1))
        self.assertEqual(g["scope"], "openid profile email")
        self.assertEqual((s["config_login_type"], s["config_oauth_redirect_host"],
                          s["config_enable_oauth_group_admin_management"], s["config_disable_standard_login"]),
                         (2, "https://cwa.lan", 1, 0))
        self.assertEqual(others, 1, "only the generic row is touched")
        self.assertNotIn("sek", r.stdout + r.stderr)

    def test_off_clears_and_restores_standard_login(self):
        self.run_script("on")
        r = self.run_script("off")
        self.assertEqual(r.returncode, 0, r.stderr)
        g, s, _ = self.rows()
        self.assertEqual((g["oauth_client_id"], g["oauth_client_secret"], g["metadata_url"], g["active"]), ("", "", "", 0))
        self.assertEqual((s["config_login_type"], s["config_enable_oauth_group_admin_management"]), (0, 0))

    def test_wrong_version_is_refused_without_writing(self):
        (self.dir / "REL").write_text("v4.1.0\n")
        r = self.run_script("on")
        self.assertNotEqual(r.returncode, 0)
        self.assertIn("verified against 4.0.8", r.stderr)
        self.assertEqual(self.rows()[1]["config_login_type"], 0)

    def test_missing_column_fails_loudly_in_both_directions(self):
        (self.dir / "app.db").unlink()
        self.make_db("id,provider_name,oauth_client_id")
        for mode in ("on", "off"):
            r = self.run_script(mode)
            self.assertNotEqual(r.returncode, 0)
            self.assertIn("lacks column", r.stderr)
            self.assertIn("re-verify", r.stderr)

    def test_missing_database_and_empty_values(self):
        r = self.run_script("on", url="")
        self.assertNotEqual(r.returncode, 0)
        self.assertIn("empty", r.stderr)
        (self.dir / "app.db").unlink()
        self.assertIn("does not exist", self.run_script("on").stderr)


if __name__ == "__main__":
    unittest.main()
