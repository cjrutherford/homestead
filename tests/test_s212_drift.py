"""S212 — Configuration Drift Detection conformance tests.

Guardrails for drift detection: managed generated files hash to
stable fingerprints, installs and configures re-baseline records
on write, removals clear them, detection names modified and
removed files with stable ordering while foreign files never
report, and unreadable files fail instead of guessing. Behavior
is covered by Go tests in internal/catalog over schema v14.
"""

import re
import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def read(rel: str) -> str:
    return (REPO / rel).read_text(encoding="utf-8")


class TestDriftDetection(unittest.TestCase):
    def test_detection_shapes(self):
        src = read("internal/catalog/drift.go")
        for symbol in ("func ManagedFiles(", "type Drift",
                       "DriftModified", "DriftRemoved",
                       "func Fingerprints(", "func RecordFingerprints(",
                       "func StoredFingerprints(",
                       "func ClearFingerprints(", "func Detect("):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)

    def test_write_paths_rebaseline(self):
        for rel, symbol in (("internal/catalog/operations.go",
                             "RecordFingerprints(st, plan.Package, dir)"),
                            ("internal/catalog/configure.go",
                             "RecordFingerprints(st, plan.Manifest.Name, dir)"),
                            ("internal/catalog/remove.go",
                             "ClearFingerprints(st, pkg)")):
            with self.subTest(rel=rel):
                self.assertIn(symbol, read(rel))

    def test_schema_v14(self):
        src = read("internal/store/store.go")
        version = int(re.search(r"(?m)^const CurrentVersion = (\d+)$", src).group(1))
        self.assertGreaterEqual(version, 14)
        self.assertIn("CREATE TABLE IF NOT EXISTS generated_fingerprints", src)


if __name__ == "__main__":
    unittest.main()
