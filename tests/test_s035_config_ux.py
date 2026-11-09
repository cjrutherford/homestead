"""S035 — Configuration UX conformance tests.

Guardrails for the config page: edit/review/apply/revert flow over the
S016 plan/apply endpoints through the S034 renderer, with diffs, notices,
and per-field errors. Component behavior is covered by Vitest specs run
via make dashboard-test.
"""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent
DASH = REPO / "dashboard"
APP = DASH / "src" / "app"


def read_dash(rel: str) -> str:
    return (DASH / rel).read_text(encoding="utf-8")


class TestConfigPage(unittest.TestCase):
    def test_page_files_exist(self):
        for name in ("config/config.ts", "config/config.html", "config/config.spec.ts"):
            with self.subTest(name=name):
                self.assertTrue((APP / name).is_file())

    def test_flow_states(self):
        src = read_dash("src/app/config/config.ts")
        for marker in ("reviewing", "candidate", "changes", "confirm()", "revert()", "notice"):
            with self.subTest(marker=marker):
                self.assertIn(marker, src)

    def test_plan_before_apply(self):
        src = read_dash("src/app/config/config.ts")
        self.assertIn("planConfig", src)
        self.assertIn("applyConfig", src)

    def test_route_and_nav(self):
        routes = read_dash("src/app/app.routes.ts")
        self.assertIn("path: 'config'", routes)
        html = read_dash("src/app/core/nav.ts")
        self.assertIn("path: '/admin/config'", html)

    def test_client_plan_apply(self):
        src = read_dash("src/app/core/api.service.ts")
        self.assertIn("planConfig(settings", src)
        self.assertIn("'/api/v1/config/plan'", src)
        self.assertIn("applyConfig(settings", src)


if __name__ == "__main__":
    unittest.main()
