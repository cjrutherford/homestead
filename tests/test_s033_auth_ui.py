"""S033 — Authentication UI conformance tests.

Guardrails for the login experience: login page with session service over
the S020 endpoints, session-aware nav, login route registered, persistence
across restarts, and no route guards (sessions are created here, not yet
required anywhere). Component behavior is covered by Vitest specs run via
make dashboard-test.
"""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent
DASH = REPO / "dashboard"
APP = DASH / "src" / "app"


def read_dash(rel: str) -> str:
    return (DASH / rel).read_text(encoding="utf-8")


class TestLoginExperience(unittest.TestCase):
    def test_login_files_exist(self):
        for name in ("login/login.ts", "login/login.html", "login/login.spec.ts",
                     "auth/session.service.ts", "auth/session.service.spec.ts"):
            with self.subTest(name=name):
                self.assertTrue((APP / name).is_file())

    def test_session_service(self):
        src = read_dash("src/app/auth/session.service.ts")
        self.assertIn("isLoggedIn", src)
        self.assertIn("localStorage", src)
        self.assertIn("homestead.session", src)
        self.assertIn("login(", src)
        self.assertIn("logout()", src)

    def test_api_session_methods(self):
        src = read_dash("src/app/core/api.service.ts")
        self.assertIn("'/api/v1/sessions'", src)
        # S256: the browser uses the HttpOnly cookie, never a bearer header.
        self.assertNotIn("Bearer", src)
        self.assertIn("logout()", src)

    def test_login_route_and_nav(self):
        routes = read_dash("src/app/app.routes.ts")
        self.assertIn("path: 'login'", routes)
        html = read_dash("src/app/app.html")
        self.assertIn('routerLink="/login"', html)
        self.assertIn("Log out", html)

    def test_login_page_flow(self):
        src = read_dash("src/app/login/login.ts")
        self.assertIn("navigateByUrl('/status')", src)
        self.assertIn("401", src)


if __name__ == "__main__":
    unittest.main()
