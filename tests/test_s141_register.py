"""S141 — Existing Mount Selection conformance tests.

Guardrails for owner-approved storage: approvals validated against
live discovery (mounted, serving usage, writable, never
pseudo-filesystems), unique owner labels with re-registration
renaming, idempotent removal, reads failing on the unregistered, and
approval metadata persisted over schema v9. Behavior is covered by
Go tests in internal/storage against fixture discovery.
"""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def read(rel: str) -> str:
    return (REPO / rel).read_text(encoding="utf-8")


class TestRegisterContract(unittest.TestCase):
    def test_register_shapes(self):
        src = read("internal/storage/register.go")
        for symbol in ("type Registration", "type Registry",
                       "func (r *Registry) Register(",
                       "func (r *Registry) Unregister(",
                       "func (r *Registry) Get(",
                       "func (r *Registry) List(",
                       "pseudo-filesystem", "ErrNotRegistered"):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)

    def test_persisted(self):
        import re
        migration = read("internal/store/store.go")
        self.assertIn("storage_registrations", migration)
        version = int(re.search(r"(?m)^const CurrentVersion = (\d+)$", migration).group(1))
        self.assertGreaterEqual(version, 9)


if __name__ == "__main__":
    unittest.main()
