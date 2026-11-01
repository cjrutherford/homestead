"""S418 - the Navidrome forward-auth declaration (ADR-055).

Checks the manifest block that the edge gate and the plan are built from.
The rendered Caddy site is covered by the Go tests; this pins the catalog
facts that need a deliberate re-check on a version bump.
"""
import re
import unittest
from pathlib import Path

import yaml

REPO = Path(__file__).resolve().parent.parent


class TestNavidromeForwardAuth(unittest.TestCase):
    def setUp(self):
        self.m = yaml.safe_load((REPO / "catalog" / "navidrome.yaml").read_text())
        self.fa = self.m["sso"]["forwardauth"]

    def test_class_and_no_oidc(self):
        self.assertEqual(self.m["sso"]["class"], "unsupported")
        self.assertNotIn("oidc", self.m["sso"])
        self.assertNotIn("protocols", self.m["sso"])

    def test_header_is_authentik_and_copied(self):
        self.assertEqual(self.fa["userheader"], "X-Authentik-Username")
        self.assertIn(self.fa["userheader"], self.fa["copyheaders"])
        for h in self.fa["copyheaders"]:
            self.assertRegex(h, r"^X-Authentik-[A-Za-z0-9-]+$")

    def test_bypass_paths_have_reasons_and_compile(self):
        paths = [b["path"] for b in self.fa["bypass"]]
        self.assertEqual(paths, [r"^/rest/.*", r"^/share/.*", r"^/app/(assets|static)/.*"])
        for b in self.fa["bypass"]:
            re.compile(b["path"])
            self.assertTrue(b["reason"].strip())

    def test_env_trusts_only_edge(self):
        self.assertEqual(self.fa["env"], {
            "ND_EXTAUTH_USERHEADER": "{{user_header}}",
            "ND_EXTAUTH_TRUSTEDSOURCES": "{{edge_source}}"})
        self.assertEqual(self.fa["admin"], "local")
        self.assertEqual(self.fa["accountsfrom"], "navidrome-sqlite")

    def test_version_pinned(self):
        self.assertIn("0.64.2", (REPO / "catalog" / "navidrome.yaml").read_text())


if __name__ == "__main__":
    unittest.main()
