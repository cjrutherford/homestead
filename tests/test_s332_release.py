"""S332 — release 0.0.1 consistency tests."""

import json
import re
import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def read(rel: str) -> str:
    return (REPO / rel).read_text(encoding="utf-8")


class TestReleaseVersion(unittest.TestCase):
    def setUp(self):
        self.version = read("VERSION").strip()

    def test_version_is_semver(self):
        self.assertRegex(self.version, r"^\d+\.\d+\.\d+$")

    def test_go_default_matches(self):
        m = re.search(r'var Version = "([^"]+)"', read("internal/version/version.go"))
        self.assertEqual(m.group(1), self.version)

    def test_dashboard_package_matches(self):
        pkg = json.loads(read("dashboard/package.json"))
        self.assertEqual(pkg["version"], self.version)

    def test_changelog_has_section(self):
        self.assertIn("\n## %s\n" % self.version, read("CHANGELOG.md"))

    def test_makefile_and_workflow(self):
        mk = read("Makefile")
        self.assertIn("VERSION := $(shell cat VERSION)", mk)
        self.assertRegex(mk, r"(?m)^RELEASE_COUNTER \?= [1-9]\d*$")
        wf = read(".github/workflows/release.yml")
        self.assertIn('v$(cat VERSION)', wf)
        self.assertIn("--draft --prerelease", wf)


if __name__ == "__main__":
    unittest.main()
