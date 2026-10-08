"""S143 — Application Directory Provisioning conformance tests.

Guardrails for safe provisioning: registered mounts only with
approval re-checked at run time, relative contained paths with
symlink components and escapes refused against live state, existing
files never converted, modes converged within 0700-0775 without
world-write, ownership explicit with untouched defaults, empty sets
and unregistered mounts refused, and per-directory runner steps
failing fast with paths named. Behavior is covered by Go tests in
internal/storage on temporary filesystems.
"""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def read(rel: str) -> str:
    return (REPO / rel).read_text(encoding="utf-8")


class TestProvisionContract(unittest.TestCase):
    def test_provision_shapes(self):
        src = read("internal/storage/provision.go")
        for symbol in ("func Provision(", "func ProvisionDefinition(",
                       "type Directory", '"storage.provision"',
                       "inside the mount", "crosses a symlink",
                       "not a directory"):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)


if __name__ == "__main__":
    unittest.main()
