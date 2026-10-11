"""S036 — Wizard Framework conformance tests.

Guardrails for resumable wizards: server-persisted step plus opaque
payload, shell with progress and back navigation, thin progress service,
and the claim wizard migrated onto both with step-only persistence (no
secrets in wizard state). Framework behavior is covered by Go tests in
internal/wizard and internal/server plus Vitest specs run via make
dashboard-test.
"""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent
DASH = REPO / "dashboard"
APP = DASH / "src" / "app"


def read(rel: str) -> str:
    return (REPO / rel).read_text(encoding="utf-8")


def read_dash(rel: str) -> str:
    return (DASH / rel).read_text(encoding="utf-8")


class TestWizardStore(unittest.TestCase):
    def test_service_operations(self):
        src = read("internal/wizard/wizard.go")
        for op in ("func (s *Service) Get(", "func (s *Service) Put(", "func (s *Service) Delete("):
            with self.subTest(op=op):
                self.assertIn(op, src)

    def test_migration_version(self):
        import re

        src = read("internal/store/store.go")
        version = int(re.search(r"(?m)^const CurrentVersion = (\d+)$", src).group(1))
        self.assertGreaterEqual(version, 4)
        self.assertIn("CREATE TABLE IF NOT EXISTS wizard_state", src)

    def test_routes(self):
        src = read("internal/server/wizards.go")
        for route in ("GET /api/v1/wizards/{id}", "PUT /api/v1/wizards/{id}",
                      "DELETE /api/v1/wizards/{id}"):
            with self.subTest(route=route):
                self.assertIn(route, src)

    def test_daemon_mounts_wizards(self):
        self.assertIn("MountWizards", read("internal/server/mount.go"))


class TestWizardFramework(unittest.TestCase):
    def test_shell_files_exist(self):
        for name in ("wizard/wizard-shell.ts", "wizard/wizard-shell.html",
                     "wizard/wizard-shell.spec.ts", "wizard/wizard.service.ts",
                     "wizard/wizard.service.spec.ts"):
            with self.subTest(name=name):
                self.assertTrue((APP / name).is_file())

    def test_shell_contract(self):
        html = read_dash("src/app/wizard/wizard-shell.html")
        self.assertIn("Step {{", html)
        self.assertIn("ng-content", html)
        self.assertIn("Back", html)
        src = read_dash("src/app/wizard/wizard-shell.ts")
        self.assertIn("steps", src)
        self.assertIn("current", src)

    def test_service_delegates(self):
        src = read_dash("src/app/wizard/wizard.service.ts")
        for method in ("load(", "save(", "clear("):
            with self.subTest(method=method):
                self.assertIn(method, src)

    def test_api_methods(self):
        src = read_dash("src/app/core/api.service.ts")
        self.assertIn("wizardState(", src)
        self.assertIn("saveWizardState(", src)
        self.assertIn("clearWizardState(", src)
        self.assertIn("/api/v1/wizards/", src)


class TestClaimMigration(unittest.TestCase):
    def test_claim_uses_framework(self):
        src = read_dash("src/app/claim/claim.ts")
        self.assertIn("WizardShell", src)
        self.assertIn("WizardService", src)
        self.assertIn("STEPS", src)

    def test_claim_persists_steps_not_secrets(self):
        """Progress saves carry step names only — never tokens or passwords."""
        src = read_dash("src/app/claim/claim.ts")
        self.assertIn("wizards.save(", src)
        self.assertNotIn("savePassword", src)
        self.assertNotIn("saveToken", src)
        self.assertNotIn("password, confirm", src.replace("submitAdmin(username: string, password: string, confirm: string)", ""))


if __name__ == "__main__":
    unittest.main()
