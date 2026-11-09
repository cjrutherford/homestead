"""S366 - BookStack with Authentik sign-in.

The Go tests run the scripts and the plan; this pins the manifest facts and
the docs.
"""
import re
import unittest
from pathlib import Path

import yaml

REPO = Path(__file__).resolve().parent.parent


def read(p):
    return (REPO / p).read_text()


class TestBookStackManifest(unittest.TestCase):
    def setUp(self):
        self.m = yaml.safe_load(read("catalog/bookstack.yaml"))
        self.svc = {s["name"]: s for s in self.m["services"]}

    def test_images_pinned_without_latest(self):
        self.assertEqual(self.m["version"], "26.09.2")
        self.assertEqual(self.svc["bookstack"]["image"], "lscr.io/linuxserver/bookstack:26.09.2")
        self.assertEqual(self.svc["bookstack"]["publisher"], "linuxserver")
        self.assertEqual(self.svc["db"]["image"], "docker.io/library/mariadb:11.8.9")
        self.assertEqual(self.svc["db"]["publisher"], "upstream")
        for s in self.svc.values():
            self.assertNotIn("latest", s["image"])

    def test_secrets_by_file_not_plaintext(self):
        env = {k: v for s in self.svc.values() for k, v in s.get("environment", {}).items()}
        for key in set(env) - {"MARIADB_RANDOM_ROOT_PASSWORD"}:
            self.assertFalse(re.search(r"PASSWORD$|SECRET$|APP_KEY$", key), key)
        consumers = {(c["service"], c["env"]) for s in self.m["secrets"] for c in s.get("consumers", [])}
        for want in (("bookstack", "FILE__DB_PASSWORD"), ("bookstack", "FILE__APP_KEY"),
                     ("bookstack", "FILE__OIDC_CLIENT_SECRET"), ("db", "MARIADB_PASSWORD_FILE")):
            self.assertIn(want, consumers)
        by = {s["name"]: s for s in self.m["secrets"]}
        self.assertEqual(by["app_key"]["encoding"], "laravel-key")
        self.assertTrue(by["admin_password"]["reveal"])

    def test_database_backup_and_config_volume(self):
        d = self.m["database"]
        self.assertEqual((d["service"], d["kind"], d["passwordsecret"]), ("db", "mariadb", "db_password"))
        self.assertEqual(self.m["backuppaths"], ["config"])
        self.assertTrue(any(v.get("database") for v in self.svc["db"]["data"]))

    def test_oidc_environment_and_role_script(self):
        e = self.m["sso"]["oidc"]["env"]
        s = e["set"]
        self.assertEqual(s["AUTH_METHOD"], "oidc")
        self.assertEqual(s["OIDC_ISSUER_DISCOVER"], "true")
        self.assertEqual(s["OIDC_USER_TO_GROUPS"], "true")
        self.assertEqual(s["OIDC_GROUPS_CLAIM"], "groups")
        self.assertEqual(s["OIDC_REMOVE_FROM_GROUPS"], "true")
        self.assertNotIn("OIDC_CLIENT_SECRET", s)
        self.assertTrue(e["exclusive"])
        self.assertEqual(e["redirectpaths"], ["/oidc/callback"])
        script = e["onapply"][2]
        self.assertEqual(script, e["onoff"][2])
        self.assertIn("$VERIFIED = 'v26.09.2'", script)
        for needle in ("information_schema.COLUMNS", "beginTransaction", "FOR UPDATE", "/app/www/version", "external_auth_id"):
            self.assertIn(needle, script)
        for bad in ("getMessage", "DELETE", "DROP", "INSERT"):
            self.assertNotIn(bad, script)

    def test_default_admin_neutralised_and_exposure_local(self):
        self.assertEqual(self.m["firstrun"]["routine"], "bookstack")
        self.assertEqual(self.m["firstrun"]["account"], "admin@admin.com")
        ep = self.m["exposure"]["endpoints"][0]
        self.assertEqual(ep["recommended"], "local")
        self.assertTrue(ep["warnings"])


class TestBookStackDocs(unittest.TestCase):
    def test_slice_doc_sections_and_live_commands(self):
        doc = read("docs/slices/S366-bookstack.md")
        for h in ("Goal", "Dependencies", "Scope", "Acceptance", "Tests", "Built", "Sources", "Verify live", "Image trust"):
            self.assertRegex(doc, r"(?m)^#+ " + h)
        for needle in ("homesteadctl apps plan", "--approve-sign-in", "--approve-access", "mariadb-dump", "/oidc/callback"):
            self.assertIn(needle, doc)

    def test_indexed_and_deferred(self):
        self.assertIn("S366", read("docs/SLICE_INDEX.md"))
        self.assertIn("S366", read("docs/DEFERRED.md"))
