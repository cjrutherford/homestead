"""S201 — Authentik Package conformance tests.

Guardrails for the fourth package: a curated manifest with pinned
non-latest images following the official compose, internal services
carrying no endpoint, a Postgres database declaration for exports,
and declared requirements, exposure, and backup paths, loading
valid from the repository catalog alongside earlier packages and
evaluating cleanly on sufficient hosts. Behavior is covered by Go
tests in internal/catalog loading the shipped directory.
"""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


class TestAuthentikContract(unittest.TestCase):
    def test_package_exists(self):
        manifest = REPO / "catalog" / "authentik.yaml"
        self.assertTrue(manifest.is_file(), "catalog/authentik.yaml missing")
        text = manifest.read_text(encoding="utf-8")
        for symbol in ("name: authentik", "version:",
                       "goauthentik/server:", "postgres:",
                       "exposes: web", "recommended: local",
                       "kind: postgres"):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, text)

    def test_no_latest_tag(self):
        text = (REPO / "catalog" / "authentik.yaml").read_text(encoding="utf-8")
        self.assertNotIn(":latest", text)


if __name__ == "__main__":
    unittest.main()
