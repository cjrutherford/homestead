"""S086 — Local Friendly Names conformance tests.

Guardrails for LAN naming: a friendly name binds a local-exposure
route to a usable LAN answer (wider exposures refused, so public names
can never arrive by accident), creation writes the proxy snippet and
the DNS answer before reloading, verification proves the route renders
and the name resolves to the bound answer through local DNS, the whole
flow planned as an idempotent runner-backed operation, and no serving
of its own. Behavior is covered by Go tests in internal/docker with
stubbed execution and httptest DNS doubles.
"""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def read(rel: str) -> str:
    return (REPO / rel).read_text(encoding="utf-8")


class TestNamesContract(unittest.TestCase):
    def test_name_shapes(self):
        src = read("internal/docker/names.go")
        for symbol in ("type FriendlyName", "type LookupFunc",
                       "func (n FriendlyName) Validate()",
                       "func (n FriendlyName) Rewrite()",
                       "func WriteNameSnippet(",
                       "func LookupVia(", "func VerifyName(",
                       "func EnsureNameDefinition(", '"names.ensure"'):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)

    def test_local_only_and_verified(self):
        src = read("internal/docker/names.go")
        for symbol in ("ExposureLocal", "must be local", "write-snippet",
                       "add-rewrite", "verify-name", "resolves to"):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)

    def test_no_serving(self):
        src = read("internal/docker/names.go")
        for banned in ("net/http", "HandleFunc"):
            with self.subTest(banned=banned):
                self.assertNotIn(banned, src)


if __name__ == "__main__":
    unittest.main()
