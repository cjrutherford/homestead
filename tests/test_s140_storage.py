"""S140 — Storage Classes conformance tests.

Guardrails for the storage vocabulary: five classes with handling
contracts (local-only databases, evictable caches, backup defaults),
unknown classes failing instead of returning permissive zeroes, and
no media-type promises discovery cannot match. Selection and
provisioning belong to later slices. Behavior is covered by Go tests
in internal/storage.
"""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def read(rel: str) -> str:
    return (REPO / rel).read_text(encoding="utf-8")


class TestStorageContract(unittest.TestCase):
    def test_class_shapes(self):
        src = read("internal/storage/classes.go")
        for symbol in ("type Class", "type Profile",
                       "func Classes()", "func ProfileFor(",
                       '"application"', '"database"', '"bulk"',
                       '"backup"', '"cache"'):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)


if __name__ == "__main__":
    unittest.main()
