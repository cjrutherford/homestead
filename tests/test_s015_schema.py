"""S015 — Configuration Schema conformance tests.

Guardrails for the schema framework: the four goal types exist, fields
carry descriptions/choices/UX hints, definition and value validation entry
points exist, settings ride the S014 envelope, and the package stays free
of serving code (endpoints are S016). Runtime behavior is covered by Go
tests in internal/schema.
"""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def read(rel: str) -> str:
    return (REPO / rel).read_text(encoding="utf-8")


class TestSchemaFramework(unittest.TestCase):
    def test_four_value_types(self):
        src = read("internal/schema/schema.go")
        for typ in ("TypeString", "TypeInteger", "TypeBoolean", "TypeChoice"):
            with self.subTest(typ=typ):
                self.assertIn(typ, src)

    def test_field_metadata_complete(self):
        """Goal: descriptions, choices, and UX hints on every field."""
        src = read("internal/schema/schema.go")
        for attr in ("Description", "Choices", "Sensitive", "Advanced", "Required", "Default"):
            with self.subTest(attr=attr):
                self.assertIn(attr, src)

    def test_validation_entry_points(self):
        src = read("internal/schema/schema.go")
        self.assertIn("func (s Schema) Validate(", src)
        self.assertIn("func (s Schema) ValidateDefinition(", src)
        self.assertIn("func (s Schema) Defaults()", src)
        self.assertIn("FieldError", src)

    def test_no_serving_code(self):
        """S015 defines and validates; S016 serves. No HTTP here."""
        src = read("internal/schema/schema.go")
        self.assertNotIn("net/http", src)
        self.assertNotIn("HandleFunc", src)

    def test_no_storage_code(self):
        """Storage stays in internal/desired (S014)."""
        src = read("internal/schema/schema.go")
        self.assertNotIn("os.CreateTemp", src)
        self.assertNotIn("os.Rename", src)


class TestDesiredIntegration(unittest.TestCase):
    def test_settings_ride_the_envelope(self):
        src = read("internal/desired/desired.go")
        self.assertIn("Settings map[string]any", src)
        self.assertIn("settings,omitempty", src)


if __name__ == "__main__":
    unittest.main()
