"""S034 — Dynamic Schema Renderer conformance tests.

Guardrails for schema-driven controls: the daemon serves field metadata,
the renderer covers every S015 type with masking, bounds, grouping, and
advanced toggling, per-field errors display, and no config page exists yet
(S035 owns it). Renderer behavior is covered by Vitest specs run via make
dashboard-test; endpoint behavior by Go tests in internal/server.
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


class TestSchemaEndpoint(unittest.TestCase):
    def test_field_json_tags(self):
        src = read("internal/schema/schema.go")
        for tag in ('`json:"name"`', '`json:"type"`', '`json:"description"`',
                    '`json:"choices,omitempty"`', '`json:"sensitive,omitempty"`'):
            with self.subTest(tag=tag):
                self.assertIn(tag, src)

    def test_schema_route_and_accessor(self):
        self.assertIn("GET /api/v1/config/schema", read("internal/server/config.go"))
        self.assertIn("func (s *Service) Schema()", read("internal/config/config.go"))


class TestRenderer(unittest.TestCase):
    def test_renderer_files_exist(self):
        for name in ("schema/schema.ts", "schema/schema-form.ts",
                     "schema/schema-form.html", "schema/schema-form.spec.ts"):
            with self.subTest(name=name):
                self.assertTrue((APP / name).is_file())

    def test_all_types_rendered(self):
        html = read_dash("src/app/schema/schema-form.html")
        src = read_dash("src/app/schema/schema-form.ts")
        self.assertIn("password", html)
        self.assertIn('type="number"', html)
        self.assertIn('type="checkbox"', html)
        self.assertIn("<select", html)
        self.assertIn("showAdvanced", src)
        self.assertIn("group", src)

    def test_errors_and_submit(self):
        src = read_dash("src/app/schema/schema-form.ts")
        self.assertIn("submitted", src)
        self.assertIn("errorFor", src)
        self.assertIn("FormData", src)

    def test_api_schema_method(self):
        src = read_dash("src/app/core/api.service.ts")
        self.assertIn("configSchema()", src)
        self.assertIn("'/api/v1/config/schema'", src)


class TestNoConfigPage(unittest.TestCase):
    def test_config_page_absent(self):
        """S035 owns the configuration page; S034 ships the renderer only."""
        src = read_dash("src/app/app.routes.ts")
        self.assertNotIn("path: 'config'", src)


if __name__ == "__main__":
    unittest.main()
