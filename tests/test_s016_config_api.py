"""S016 — Configuration API conformance tests.

Guardrails for the validated configuration surface: the service composes
schema validation with desired storage, the daemon mounts exactly the
read/plan/apply routes, the production wiring stays schema-free until
domain slices register fields, and API errors serialize lowercase.
Runtime behavior is covered by Go tests in internal/config and
internal/server.
"""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def read(rel: str) -> str:
    return (REPO / rel).read_text(encoding="utf-8")


class TestConfigService(unittest.TestCase):
    def test_service_composes_schema_and_storage(self):
        src = read("internal/config/config.go")
        self.assertIn("homestead/internal/schema", src)
        self.assertIn("homestead/internal/desired", src)
        for op in ("func (s *Service) Get(", "func (s *Service) Plan(", "func (s *Service) Apply("):
            with self.subTest(op=op):
                self.assertIn(op, src)

    def test_apply_persists_only_when_valid(self):
        src = read("internal/config/config.go")
        self.assertLess(src.index("plan.Errors"), src.index("desired.Save"))

    def test_missing_file_means_unconfigured(self):
        src = read("internal/config/config.go")
        self.assertIn("desired.ErrNotFound", src)
        self.assertIn("desired.New()", src)


class TestApiRoutes(unittest.TestCase):
    def test_three_routes_mounted(self):
        src = read("internal/server/config.go")
        for route in (
            "GET /api/v1/config",
            "POST /api/v1/config/plan",
            "PUT /api/v1/config",
        ):
            with self.subTest(route=route):
                self.assertIn(route, src)

    def test_validation_failures_are_422(self):
        src = read("internal/server/config.go")
        self.assertIn("StatusUnprocessableEntity", src)

    def test_malformed_bodies_are_400(self):
        src = read("internal/server/config.go")
        self.assertIn("StatusBadRequest", src)
        self.assertIn("DisallowUnknownFields", src)

    def test_production_wiring_registers_domain_schema(self):
        """Domain slices register fields; S057 added the firewall section."""
        main = read("cmd/homestead-server/main.go")
        mount = read("internal/server/mount.go")
        self.assertIn("firewall.Schema()", mount)
        self.assertIn("MountConfig", mount)
        self.assertIn("desired.DefaultPath()", main)

    def test_errors_serialize_lowercase(self):
        src = read("internal/schema/schema.go")
        self.assertIn('`json:"field"`', src)
        self.assertIn('`json:"reason"`', src)


if __name__ == "__main__":
    unittest.main()
