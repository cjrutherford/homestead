"""S032 — Claim Wizard conformance tests.

Guardrails for the onboarding wizard: token then first-administrator
steps against the S019/S020 endpoints, claim route and nav registered,
no login/session UI (S033 owns authentication), and no server changes
(the wizard reuses existing endpoints). Component behavior is covered by
Vitest specs run via make dashboard-test.
"""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent
DASH = REPO / "dashboard"
APP = DASH / "src" / "app"


def read_dash(rel: str) -> str:
    return (DASH / rel).read_text(encoding="utf-8")


class TestWizard(unittest.TestCase):
    def test_wizard_files_exist(self):
        for name in ("claim.ts", "claim.html", "claim.spec.ts"):
            with self.subTest(name=name):
                self.assertTrue((APP / "claim" / name).is_file())

    def test_wizard_steps(self):
        src = read_dash("src/app/claim/claim.ts")
        for step in ("'token'", "'admin'", "'done'"):
            with self.subTest(step=step):
                self.assertIn(step, src)

    def test_wizard_uses_claim_and_setup_endpoints(self):
        src = read_dash("src/app/claim/claim.ts")
        self.assertIn("this.api.claim(", src)
        self.assertIn("this.api.setupAdmin(", src)

    def test_api_service_methods(self):
        src = read_dash("src/app/core/api.service.ts")
        self.assertIn("claim(token: string)", src)
        self.assertIn("'/api/v1/claim'", src)
        self.assertIn("setupAdmin(username: string, password: string)", src)
        self.assertIn("'/api/v1/admins/setup'", src)

    def test_route_and_nav(self):
        routes = read_dash("src/app/app.routes.ts")
        self.assertIn("path: 'claim'", routes)
        html = read_dash("src/app/app.html")
        self.assertIn('routerLink="/claim"', html)


class TestLaterSlicesAbsent(unittest.TestCase):
    def test_no_login_or_session_ui(self):
        """S033 owns authentication UI; no session handling here."""
        names = [p.name for p in APP.rglob("*")]
        for banned in ("login", "session"):
            with self.subTest(banned=banned):
                self.assertNotIn(banned, " ".join(names))
        src = read_dash("src/app/core/api.service.ts")
        self.assertNotIn("/api/v1/sessions", src)


if __name__ == "__main__":
    unittest.main()
