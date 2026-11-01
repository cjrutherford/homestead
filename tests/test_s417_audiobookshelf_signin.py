"""S417 — Audiobookshelf sign-in script behaviour.

The manifest's inline node script writes Audiobookshelf's own
server-settings row (table settings) through node:sqlite. This runs it
against a temporary database with the 2.37.1 table layout and settings
shape (verified from a fresh ghcr.io/advplyr/audiobookshelf:2.37.1
container). Needs a node with node:sqlite (22.5+); skipped without one.
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
SCHEMA = ("CREATE TABLE `settings` (`key` VARCHAR(255) PRIMARY KEY, `value` JSON, "
          "`createdAt` DATETIME NOT NULL, `updatedAt` DATETIME NOT NULL)")
STAMP = "2026-11-01 01:54:32.461 +00:00"
CLIENT = "homestead-audiobookshelf"
BASE = "https://h.lan/apps/auth/application/o/"
VALUES = {"client_id": CLIENT, "client_secret": "sek-ret", "issuer": BASE + CLIENT + "/",
          "authorize_url": BASE + "authorize/", "token_url": BASE + "token/",
          "userinfo_url": BASE + "userinfo/", "jwks_url": BASE + CLIENT + "/jwks/",
          "logout_url": BASE + CLIENT + "/end-session/"}


def fresh_settings():
    """The server-settings JSON of a fresh 2.37.1 server (auth fields as shipped)."""
    return {"id": "server-settings", "tokenSecret": "tok-xyz", "language": "en-us", "logLevel": 2,
            "backupSchedule": False, "version": "2.37.1", "buildNumber": 1,
            "authLoginCustomMessage": None, "authActiveAuthMethods": ["local"],
            "authOpenIDIssuerURL": None, "authOpenIDAuthorizationURL": None, "authOpenIDTokenURL": None,
            "authOpenIDUserInfoURL": None, "authOpenIDJwksURL": None, "authOpenIDLogoutURL": None,
            "authOpenIDClientID": None, "authOpenIDClientSecret": None,
            "authOpenIDTokenSigningAlgorithm": "RS256", "authOpenIDButtonText": "Login with OpenId",
            "authOpenIDAutoLaunch": False, "authOpenIDAutoRegister": False,
            "authOpenIDMatchExistingBy": None, "authOpenIDMobileRedirectURIs": ["audiobookshelf://oauth"],
            "authOpenIDGroupClaim": "", "authOpenIDAdvancedPermsClaim": ""}


def node_has_sqlite():
    node = shutil.which("node")
    if not node:
        return False
    return subprocess.run([node, "--no-warnings", "-e", "require('node:sqlite')"], capture_output=True).returncode == 0


@unittest.skipUnless(node_has_sqlite(), "needs node with node:sqlite")
class TestAudiobookshelfSignInScript(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        m = yaml.safe_load((REPO / "catalog" / "audiobookshelf.yaml").read_text())
        env = m["sso"]["oidc"]["env"]
        cls.on, cls.off = env["onapply"], env["onoff"]
        cls.script = cls.on[2]
        assert cls.off[2] == cls.script and cls.on[-1] == "on" and cls.off[-1] == "off"
        assert cls.on[:2] == ["node", "-e"] and len(cls.on) == 4

    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.dir = Path(self.tmp.name)
        self.addCleanup(self.tmp.cleanup)
        self.pkg("2.37.1")
        self.signin(VALUES)
        self.make_db()

    def pkg(self, version):
        (self.dir / "package.json").write_text(json.dumps({"name": "audiobookshelf", "version": version}))

    def signin(self, values):
        (self.dir / "sf.json").write_text(json.dumps(values))

    def make_db(self, settings=None, schema=SCHEMA, row=True):
        p = self.dir / "abs.sqlite"
        if p.exists():
            p.unlink()
        db = sqlite3.connect(p)
        db.execute(schema)
        db.execute("create table users(id)")
        if row:
            db.execute("insert into settings values('server-settings',?,?,?)",
                       (json.dumps(settings or fresh_settings()), STAMP, STAMP))
        db.commit()
        db.close()

    def run_script(self, mode):
        src = (self.script.replace("'/config/absdatabase.sqlite'", json.dumps(str(self.dir / "abs.sqlite")))
               .replace("'/app/package.json'", json.dumps(str(self.dir / "package.json"))))
        env = dict(os.environ, ABS_SIGNIN_FILE=str(self.dir / "sf.json"))
        return subprocess.run(["node", "--no-warnings", "-e", src, mode], env=env, capture_output=True, text=True)

    def row(self):
        db = sqlite3.connect(self.dir / "abs.sqlite")
        r = db.execute("select value, createdAt, updatedAt from settings where key='server-settings'").fetchone()
        db.close()
        return json.loads(r[0]), r[1], r[2]

    def raw(self):
        db = sqlite3.connect(self.dir / "abs.sqlite")
        r = db.execute("select value from settings").fetchone()[0]
        db.close()
        return r

    def assertRefused(self, r, text):
        self.assertEqual(r.returncode, 1, r.stdout + r.stderr)
        self.assertIn(text, r.stderr)
        self.assertIn("re-verify", r.stderr)
        self.assertNotIn("sek-ret", r.stdout + r.stderr)

    def test_on_writes_the_auth_fields_and_keeps_the_rest(self):
        before = fresh_settings()
        for _ in range(2):  # idempotent
            r = self.run_script("on")
            self.assertEqual(r.returncode, 0, r.stderr)
            self.assertEqual(r.stdout.strip(), "audiobookshelf sign-in on")
            self.assertNotIn("sek-ret", r.stdout + r.stderr)
        s, created, updated = self.row()
        self.assertEqual(s["authActiveAuthMethods"], ["local", "openid"])
        want = {"authOpenIDIssuerURL": VALUES["issuer"], "authOpenIDAuthorizationURL": VALUES["authorize_url"],
                "authOpenIDTokenURL": VALUES["token_url"], "authOpenIDUserInfoURL": VALUES["userinfo_url"],
                "authOpenIDJwksURL": VALUES["jwks_url"], "authOpenIDLogoutURL": VALUES["logout_url"],
                "authOpenIDClientID": CLIENT, "authOpenIDClientSecret": "sek-ret",
                "authOpenIDTokenSigningAlgorithm": "RS256", "authOpenIDAutoLaunch": False,
                "authOpenIDAutoRegister": True, "authOpenIDMatchExistingBy": None,
                "authOpenIDMobileRedirectURIs": ["audiobookshelf://oauth"], "authOpenIDGroupClaim": "groups",
                "authOpenIDAdvancedPermsClaim": "", "authOpenIDSubfolderForRedirectURLs": ""}
        for k, v in want.items():
            self.assertEqual(s[k], v, k)
        for k in ("tokenSecret", "language", "logLevel", "backupSchedule", "version", "buildNumber", "authLoginCustomMessage"):
            self.assertEqual(s[k], before[k], k)
        self.assertEqual(created, STAMP)
        self.assertNotEqual(updated, STAMP)
        self.assertRegex(updated, r"^\d{4}-\d\d-\d\d \d\d:\d\d:\d\d\.\d{3} \+00:00$")

    def test_off_clears_sign_in_and_keeps_local(self):
        self.assertEqual(self.run_script("on").returncode, 0)
        for _ in range(2):
            r = self.run_script("off")
            self.assertEqual(r.returncode, 0, r.stderr)
        s, _, _ = self.row()
        self.assertEqual(s["authActiveAuthMethods"], ["local"])
        for k in ("authOpenIDIssuerURL", "authOpenIDAuthorizationURL", "authOpenIDTokenURL", "authOpenIDUserInfoURL",
                  "authOpenIDJwksURL", "authOpenIDLogoutURL", "authOpenIDClientID", "authOpenIDClientSecret"):
            self.assertIsNone(s[k], k)
        self.assertFalse(s["authOpenIDAutoRegister"])
        self.assertEqual(s["authOpenIDGroupClaim"], "")
        self.assertNotIn("authOpenIDSubfolderForRedirectURLs", s)
        self.assertNotIn("sek-ret", self.raw())
        self.assertEqual(s["tokenSecret"], "tok-xyz")

    def test_local_stays_even_if_it_was_missing_or_other_methods_exist(self):
        st = fresh_settings()
        st["authActiveAuthMethods"] = ["openid"]
        self.make_db(st)
        self.assertEqual(self.run_script("on").returncode, 0)
        self.assertEqual(self.row()[0]["authActiveAuthMethods"], ["local", "openid"])
        self.assertEqual(self.run_script("off").returncode, 0)
        self.assertEqual(self.row()[0]["authActiveAuthMethods"], ["local"])

    def test_a_foreign_openid_setup_is_never_touched(self):
        st = fresh_settings()
        st.update(authActiveAuthMethods=["local", "openid"], authOpenIDClientID="owner-made", authOpenIDClientSecret="theirs")
        self.make_db(st)
        before = self.raw()
        self.assertRefused(self.run_script("on"), "did not create")
        self.assertEqual(self.raw(), before)
        r = self.run_script("off")
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertIn("left alone", r.stdout)
        self.assertEqual(self.raw(), before)

    def test_another_version_is_refused_on_but_off_still_works(self):
        self.assertEqual(self.run_script("on").returncode, 0)
        self.pkg("2.38.0")
        before = self.raw()
        self.assertRefused(self.run_script("on"), "verified against 2.37.1")
        self.assertEqual(self.raw(), before)
        r = self.run_script("off")  # turning sign-in off must always be possible
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertEqual(self.row()[0]["authActiveAuthMethods"], ["local"])

    def test_unreadable_version_is_refused(self):
        (self.dir / "package.json").unlink()
        self.assertRefused(self.run_script("on"), "cannot read the Audiobookshelf version")

    def test_schema_and_shape_guards(self):
        self.make_db(schema="CREATE TABLE settings (key VARCHAR(255) PRIMARY KEY, value JSON, createdAt DATETIME NOT NULL)", row=False)
        self.assertRefused(self.run_script("on"), "lacks column updatedAt")
        self.make_db(row=False)
        self.assertRefused(self.run_script("on"), "no server-settings row")
        st = fresh_settings()
        del st["authOpenIDGroupClaim"]
        self.make_db(st)
        before = self.raw()
        self.assertRefused(self.run_script("on"), "lacks the field authOpenIDGroupClaim")
        self.assertEqual(self.raw(), before)
        st = fresh_settings()
        st["authActiveAuthMethods"] = "local"
        self.make_db(st)
        self.assertRefused(self.run_script("on"), "unexpected shape")
        db = sqlite3.connect(self.dir / "abs.sqlite")
        db.execute("update settings set value='not json'")
        db.commit()
        db.close()
        self.assertRefused(self.run_script("on"), "not JSON")
        (self.dir / "abs.sqlite").unlink()
        self.assertRefused(self.run_script("on"), "does not exist")

    def test_missing_or_wrong_values_change_nothing(self):
        self.make_db()
        before = self.raw()
        for bad in ({}, dict(VALUES, client_secret=""), dict(VALUES, jwks_url=""), dict(VALUES, client_id="other")):
            self.signin(bad)
            r = self.run_script("on")
            self.assertEqual(r.returncode, 1, r.stdout + r.stderr)
            self.assertEqual(self.raw(), before)
        (self.dir / "sf.json").write_text('{"client_secret":"sek-ret" not json')
        r = self.run_script("on")
        self.assertRefused(r, "missing or unreadable")  # a parse error must not quote the file
        (self.dir / "sf.json").unlink()
        self.assertRefused(self.run_script("on"), "missing or unreadable")

    def test_bad_mode(self):
        self.assertRefused(self.run_script("sideways"), "expected the argument on or off")

    def test_a_failed_write_rolls_back(self):
        db = sqlite3.connect(self.dir / "abs.sqlite")
        db.execute("create trigger nope before update on settings begin select raise(abort, 'sek-ret blocked'); end")
        db.commit()
        db.close()
        before = self.raw()
        r = self.run_script("on")
        self.assertEqual(r.returncode, 1)
        self.assertIn("the database write failed", r.stderr)
        self.assertNotIn("sek-ret", r.stdout + r.stderr)
        self.assertEqual(self.raw(), before)


if __name__ == "__main__":
    unittest.main()
