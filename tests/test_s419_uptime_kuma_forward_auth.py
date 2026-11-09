"""S419 - the Uptime Kuma forward-auth declaration (ADR-055).

Kuma is gated at the edge for Authentik administrators only (no copied header).
Its own login was kept at S419 and turned off at S422 (see test_s422). The rendered
Caddy site is covered by the Go tests; this pins the catalog facts that need a
deliberate re-check on a version bump.
"""
import re
import unittest
from pathlib import Path

import yaml

REPO = Path(__file__).resolve().parent.parent
OPEN = [r"^/api/push/.*", r"^/status/.*", r"^/api/status-page/.*", r"^/api/badge/.*",
        r"^/assets/.*", r"^/upload/.*", r"^/icon\.svg$"]


class TestKumaForwardAuth(unittest.TestCase):
    def setUp(self):
        self.text = (REPO / "catalog" / "uptime-kuma.yaml").read_text()
        self.m = yaml.safe_load(self.text)
        self.fa = self.m["sso"]["forwardauth"]

    def test_class_and_no_oidc(self):
        self.assertEqual(self.m["sso"]["class"], "unsupported")
        self.assertNotIn("oidc", self.m["sso"])

    def test_admin_only_and_no_header_or_env(self):
        self.assertEqual(self.fa["access"], "admin-only")
        self.assertEqual(self.fa["service"], "uptime-kuma")
        for k in ("userheader", "copyheaders", "env", "accountsfrom"):
            self.assertNotIn(k, self.fa)

    def test_bypass_paths_exact_and_reasoned(self):
        self.assertEqual([b["path"] for b in self.fa["bypass"]], OPEN)
        for b in self.fa["bypass"]:
            re.compile(b["path"])
            self.assertTrue(b["reason"].strip())

    def test_never_bypassed_paths(self):
        for never in ["/metrics", "/dashboard", "/socket.io/", "/status-page", "/manage-status-page", "/"]:
            for b in self.fa["bypass"]:
                self.assertIsNone(re.match(b["path"], never), (b["path"], never))

    def test_no_host_port_published(self):
        reqs = self.m["requirements"]
        self.assertFalse(reqs.get("ports"))
        for s in self.m["services"]:
            self.assertNotIn("ports", s)
        self.assertEqual(self.m["services"][0]["containerport"], 3001)

    def test_still_local_only(self):
        ep = self.m["exposure"]["endpoints"][0]
        self.assertEqual(ep["recommended"], "local")
        self.assertFalse(ep["allowwider"])

    def test_database_write_is_the_s422_block_only(self):
        # S422 replaced "no database write": the only write is the pinned
        # ownloginoff block, which needs the host block.
        self.assertIn("ownloginoff", self.fa)
        self.assertTrue(self.fa["requireshostblock"])

    def test_version_pinned(self):
        self.assertEqual(self.m["version"], "2.5.7")


if __name__ == "__main__":
    unittest.main()
