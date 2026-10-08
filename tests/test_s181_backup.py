"""S181 — Local Backup Destination conformance tests.

Guardrails for local backups: destinations resolving from backup
class assignments with live re-checks, source trees validated
against mounts, modes preserved with symlinks refused before
walking, missing sources failing loudly instead of backing up
nothing, manifests landing last with re-walked counts, single
timestamps per run, and runner-backed execution failing fast.
Behavior is covered by Go tests in internal/backup on temporary
filesystems.
"""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def read(rel: str) -> str:
    return (REPO / rel).read_text(encoding="utf-8")


class TestBackupContract(unittest.TestCase):
    def test_backup_shapes(self):
        src = read("internal/backup/backup.go")
        for symbol in ("func ResolveDestination(", "func Definition(",
                       "type Request", "type Manifest",
                       '"backup.run"', "backup.json"):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)


if __name__ == "__main__":
    unittest.main()
