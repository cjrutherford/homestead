"""S233 — Docker-only Emergency Recovery conformance tests.

Guardrails for the ownership receipt: every curated stack renders
vanilla Compose with no daemon, store, or proprietary extensions
(proven against the real `compose config`, skipped only without
an engine), layered overrides merge as one document, and the
emergency runbook names stack locations, the external network
prerequisite, data and secret handling, and the boundary of what
Compose-only operation does not cover. Behavior is covered by Go
tests in internal/catalog.
"""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def read(rel: str) -> str:
    return (REPO / rel).read_text(encoding="utf-8")


class TestEmergencyRecovery(unittest.TestCase):
    def test_compose_validation(self):
        src = read("internal/catalog/recovery_test.go")
        for symbol in ("TestGeneratedStacksValidateWithCompose",
                       "TestOverrideMergesWithCompose",
                       '"docker", "compose"',
                       "homestead-edge"):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)

    def test_no_proprietary_extensions(self):
        src = read("internal/catalog/compose.go")
        self.assertNotIn("x-homestead", src)

    def test_runbook(self):
        docs = read("docs/DEVELOPMENT.md")
        for symbol in ("Docker-only emergency recovery (S233)",
                       "/opt/homestead/stacks/",
                       "docker network create homestead-edge",
                       "compose.override.yaml",
                       "S230", "S234"):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, docs)


if __name__ == "__main__":
    unittest.main()
