"""S194 — Platform Update Model conformance tests.

The platform model names Homestead's own components apart from
catalog applications: the dashboard rides daemon releases because
its assets embed in the binary, desired versions arrive
owner-declared with no hosted feed, downgrades and strangers
refuse, and the application apply route turns platform names away
so compose flows never try to replace the daemon. Behavior is
covered by Go tests in internal/update and internal/server.
"""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def read(rel: str) -> str:
    return (REPO / rel).read_text(encoding="utf-8")


class TestPlatformModel(unittest.TestCase):
    def test_model_shapes(self):
        src = read("internal/update/platform.go")
        for symbol in ("ComponentDaemon",
                       "ComponentCLI",
                       "IsPlatformComponent",
                       "PlatformRelease",
                       "PlatformPlan",
                       "PlanPlatform",
                       "downgrade"):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)

    def test_boundary_guard(self):
        src = read("internal/server/updates.go")
        for symbol in ("IsPlatformComponent",
                       "updates separately from applications"):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)

    def test_no_platform_route_leak(self):
        matrix = read("docs/PARITY.md")
        for symbol in ("| /api/v1/updates |", "| /api/v1/updates/{name}/apply |"):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, matrix)
        self.assertNotIn("/api/v1/platform", matrix)


if __name__ == "__main__":
    unittest.main()
