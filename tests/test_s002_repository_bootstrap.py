"""S002 — Repository Bootstrap conformance tests.

Verifies the bootstrap layout: Go module with both command placeholders and
a version package, the pnpm-managed Angular dashboard scaffold, the ignore
rules for build outputs, and the CI pipeline covering both stacks.

Static file assertions only; the actual compilers and package managers run
via make targets and CI.
"""

import json
import re
import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def read(rel: str) -> str:
    return (REPO / rel).read_text(encoding="utf-8")


class TestGoBootstrap(unittest.TestCase):
    def test_go_mod_declares_module(self):
        content = read("go.mod")
        self.assertRegex(content, r"(?m)^module homestead$")

    def test_command_placeholders_exist(self):
        for cmd in ("homestead-server", "homesteadctl"):
            with self.subTest(cmd=cmd):
                main_go = REPO / "cmd" / cmd / "main.go"
                self.assertTrue(main_go.is_file(), f"cmd/{cmd}/main.go must exist")
                self.assertIn("package main", main_go.read_text(encoding="utf-8"))

    def test_placeholders_reference_version_package(self):
        for cmd in ("homestead-server", "homesteadctl"):
            with self.subTest(cmd=cmd):
                sources = " ".join(
                    p.read_text(encoding="utf-8")
                    for p in (REPO / "cmd" / cmd).glob("*.go")
                    if not p.name.endswith("_test.go")
                )
                self.assertIn("homestead/internal/version", sources)

    def test_version_package_has_test(self):
        self.assertTrue((REPO / "internal" / "version" / "version.go").is_file())
        self.assertTrue((REPO / "internal" / "version" / "version_test.go").is_file())

    def test_no_external_go_dependencies(self):
        """Direct deps stay on the slice-approved allowlist (ADR-001).

        Indirect transitive deps are fine; each new direct dependency needs
        its owning slice. Approved: S013 SQLite driver, S014 YAML.
        """
        import re

        direct = re.findall(r"(?m)^\t(\S+) v\S+$", read("go.mod"))
        self.assertEqual(
            sorted(direct), ["gopkg.in/yaml.v3", "modernc.org/sqlite"]
        )


class TestDashboardBootstrap(unittest.TestCase):
    def test_package_json_names_pinned_toolchain(self):
        pkg = json.loads(read("dashboard/package.json"))
        self.assertEqual(pkg["name"], "homestead-dashboard")
        self.assertIn("build", pkg["scripts"])
        self.assertIn("@angular/cli", pkg["devDependencies"])
        self.assertTrue(pkg.get("packageManager", "").startswith("pnpm@"))

    def test_angular_project_uses_pnpm(self):
        angular = json.loads(read("dashboard/angular.json"))
        self.assertEqual(angular["cli"]["packageManager"], "pnpm")
        self.assertIn("homestead-dashboard", angular["projects"])

    def test_typescript_configs_present(self):
        for cfg in ("tsconfig.json", "tsconfig.app.json"):
            with self.subTest(cfg=cfg):
                self.assertTrue((REPO / "dashboard" / cfg).is_file())

    def test_lockfile_committed(self):
        self.assertTrue((REPO / "dashboard" / "pnpm-lock.yaml").is_file())

    def test_build_script_allowlist_checked_in(self):
        """pnpm 12 blocks install scripts by default; approvals must be explicit."""
        workspace = read("dashboard/pnpm-workspace.yaml")
        self.assertIn("allowBuilds", workspace)
        self.assertIn("esbuild", workspace)


class TestIgnoreRules(unittest.TestCase):
    def test_gitignore_covers_build_outputs(self):
        ignore = read(".gitignore")
        for pattern in ("node_modules/", "dashboard/dist/", "dashboard/.angular/"):
            with self.subTest(pattern=pattern):
                self.assertIn(pattern, ignore)


class TestPipeline(unittest.TestCase):
    def test_makefile_targets_exist(self):
        makefile = read("Makefile")
        for target in (
            "test-py",
            "test-go",
            "build",
            "lint",
            "dashboard-install",
            "dashboard-build",
        ):
            with self.subTest(target=target):
                self.assertRegex(makefile, rf"(?m)^{target}:")

    def test_ci_covers_both_stacks(self):
        ci = read(".github/workflows/ci.yml")
        self.assertIn("make lint", ci)
        self.assertIn("make build", ci)
        self.assertIn("make test", ci)
        self.assertIn("dashboard-install", ci)
        self.assertIn("dashboard-build", ci)
        self.assertIn("setup-go", ci)
        self.assertIn("setup-node", ci)

    def test_docs_cover_local_workflow(self):
        dev = read("docs/DEVELOPMENT.md")
        self.assertIn("make check", dev)
        self.assertIn("pnpm", dev)


if __name__ == "__main__":
    unittest.main()
