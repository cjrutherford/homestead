"""S470 - shared-origin hardening: names, edge strips, headers, docs."""

import re
import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def read(rel):
    return (REPO / rel).read_text(encoding="utf-8")


class SharedOriginHardening(unittest.TestCase):
    def test_cookie_names_use_host_prefix(self):
        auth = read("internal/server/auth.go")
        self.assertIn('HostSessionCookieName = "__Host-" + SessionCookieName', auth)
        self.assertIn('HostCSRFCookieName    = "__Host-" + CSRFCookieName', auth)
        # Reads go through the scheme-aware helper, never the bare name.
        for rel in ("internal/server/auth.go", "internal/server/dashsocket.go"):
            code = re.sub(r"//.*", "", read(rel))
            self.assertNotIn("r.Cookie(SessionCookieName)", code, rel)
        ts = read("dashboard/src/app/core/auth.interceptor.ts")
        self.assertIn("'__Host-homestead_csrf'", ts)

    def test_edge_strips_and_headers(self):
        routes = read("internal/docker/routes.go")
        for want in (
            "request_header Cookie",
            "header_down Set-Cookie",
            "frame-ancestors 'self'",
            "Referrer-Policy same-origin",
            '"authentik_session", "authentik_csrf"',
            '"__Host-homestead_session", "__Host-homestead_csrf"',
        ):
            self.assertIn(want, routes)
        self.assertIn("p.Path != SignInPath", routes)  # Authentik keeps its cookies

    def test_dashboard_not_framable(self):
        front = read("internal/server/dashsocket.go")
        self.assertIn("frame-ancestors 'none'", front)
        self.assertIn('"X-Frame-Options", "DENY"', front)

    def test_slice_docs(self):
        doc = read("docs/slices/S470-shared-origin-hardening.md")
        self.assertIn("Re-login needed once", doc)
        self.assertIn("S470", read("docs/SLICE_INDEX.md"))
        self.assertIn("S470", read("docs/DEFERRED.md"))


if __name__ == "__main__":
    unittest.main()
