"""S180 — Backup Contract conformance tests.

Guardrails for backup declarations: database exports per engine
with cross-engine fields refused and services linked, consistency
hooks staged with commands bounded and services linked, manifest
integration with backward-compatible absence, and credentials kept
out of declarations. Behavior is covered by Go tests in
internal/catalog.
"""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def read(rel: str) -> str:
    return (REPO / rel).read_text(encoding="utf-8")


class TestBackupContract(unittest.TestCase):
    def test_backup_shapes(self):
        src = read("internal/catalog/backup.go")
        for symbol in ("type DatabaseBackup", "type BackupHook",
                       "func (d DatabaseBackup) Validate(",
                       "func (h BackupHook) Validate(",
                       '"sqlite"', '"postgres"',
                       '"pre"', '"post"'):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)


if __name__ == "__main__":
    unittest.main()
