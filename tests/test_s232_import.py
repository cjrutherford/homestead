"""S232 — Configuration Import conformance tests.

Guardrails for validating and importing exported state: version
bounds plus schema compatibility refuse newer documents, secret
completeness is proven before anything writes (missing names
listed, blanks counted missing, extras refused), secrets set
before the desired file lands so failures leave no half-imported
state, invalid names fail before the file lands, re-runs
converge, and values decrypt under a fresh key (recovery, not
copying). Behavior is covered by Go tests in internal/desired.
"""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def read(rel: str) -> str:
    return (REPO / rel).read_text(encoding="utf-8")


class TestConfigImport(unittest.TestCase):
    def test_import_shapes(self):
        src = read("internal/desired/import.go")
        for symbol in ("type ImportSummary", "func ImportState(",
                       "missing secret values", "unexpected secret value",
                       "upgrade homestead"):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)

    def test_secrets_before_file(self):
        src = read("internal/desired/import.go")
        self.assertLess(src.index("secrets.Set("),
                        src.index("Save(desiredPath"))


if __name__ == "__main__":
    unittest.main()
