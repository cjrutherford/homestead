"""S030 — Angular Shell conformance tests.

Guardrails for the dashboard shell: brand header with nav plus outlet,
status/redirect/wildcard routing, a typed relative-URL API service, the
Vitest runner wired into make and CI, and no later-slice pages (claim in
S032, authentication in S033, configuration UX in S035). Runtime behavior
is covered by Vitest specs run via make dashboard-test.
"""

import json
import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent
DASH = REPO / "dashboard"
APP = DASH / "src" / "app"


def read_dash(rel: str) -> str:
    return (DASH / rel).read_text(encoding="utf-8")


class TestShell(unittest.TestCase):
    def test_shell_layout(self):
        html = read_dash("src/app/app.html")
        self.assertIn("Homestead", html)
        self.assertIn("routerLink", html)
        self.assertIn("router-outlet", html)

    def test_shell_component(self):
        src = read_dash("src/app/app.ts")
        self.assertIn("RouterOutlet", src)
        self.assertIn("RouterLink", src)

    def test_routes(self):
        src = read_dash("src/app/app.routes.ts")
        self.assertIn("redirectTo", src)
        self.assertIn("'status'", src)
        self.assertIn("'**'", src)

    def test_status_and_not_found_pages(self):
        for page in ("status/status.ts", "not-found/not-found.ts"):
            with self.subTest(page=page):
                self.assertTrue((APP / page).is_file())


class TestApiService(unittest.TestCase):
    def test_typed_api_methods(self):
        src = read_dash("src/app/core/api.service.ts")
        for method in ("health()", "identity()", "config()"):
            with self.subTest(method=method):
                self.assertIn(method, src)

    def test_relative_urls(self):
        """Same-origin in production (S031); proxied in development."""
        src = read_dash("src/app/core/api.service.ts")
        for url in ("'/healthz'", "'/api/v1/identity'", "'/api/v1/config'"):
            with self.subTest(url=url):
                self.assertIn(url, src)
        self.assertNotIn("localhost", src)
        self.assertNotIn("127.0.0.1", src)

    def test_dev_proxy(self):
        proxy = json.loads(read_dash("proxy.conf.json"))
        for prefix in ("/healthz", "/version", "/api"):
            with self.subTest(prefix=prefix):
                self.assertIn(prefix, proxy)
                self.assertIn("127.0.0.1:8080", proxy[prefix]["target"])


class TestRunnerWired(unittest.TestCase):
    def test_unit_test_target(self):
        angular = json.loads(read_dash("angular.json"))
        test = angular["projects"]["homestead-dashboard"]["architect"]["test"]
        self.assertEqual(test["builder"], "@angular/build:unit-test")

    def test_vitest_dependency(self):
        pkg = json.loads(read_dash("package.json"))
        self.assertIn("vitest", pkg["devDependencies"])

    def test_make_and_ci_run_tests(self):
        self.assertIn("dashboard-test", (REPO / "Makefile").read_text(encoding="utf-8"))
        self.assertIn("dashboard-test", (REPO / ".github/workflows/ci.yml").read_text(encoding="utf-8"))


class TestLaterSlicesAbsent(unittest.TestCase):
    def test_no_auth_config_pages(self):
        """S033 authentication, S035 configuration own their pages (S032 landed claim)."""
        names = [p.name for p in APP.rglob("*")]
        for banned in ("login", "setup", "config-page"):
            with self.subTest(banned=banned):
                self.assertNotIn(banned, " ".join(names))

    def test_no_future_routes(self):
        src = read_dash("src/app/app.routes.ts")
        for path in ("path: 'login'", "path: 'setup'", "path: 'config'"):
            with self.subTest(path=path):
                self.assertNotIn(path, src)


if __name__ == "__main__":
    unittest.main()
