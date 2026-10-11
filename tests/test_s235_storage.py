"""S235 — Storage Setup Surfaces conformance tests.

Guardrails for mount registration and class assignment over the
API and CLI: discovered mounts list live candidates, phantom
paths refuse against live discovery, unknown classes refuse
before touching storage, unregistered mounts refuse assignment,
and lists read empty (never null). Behavior is covered by Go
tests in internal/server and cmd/homesteadctl.
"""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def read(rel: str) -> str:
    return (REPO / rel).read_text(encoding="utf-8")


class TestStorageSurfaces(unittest.TestCase):
    def test_api_shapes(self):
        src = read("internal/server/storage.go")
        for symbol in ("GET /api/v1/storage/mounts",
                       "GET /api/v1/storage/registrations",
                       "POST /api/v1/storage/registrations",
                       "GET /api/v1/storage/assignments",
                       "POST /api/v1/storage/assignments",
                       "func (s *Server) MountStorage("):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)

    def test_cli_shapes(self):
        src = read("cmd/homesteadctl/storage.go")
        for symbol in ('"mounts"', '"register"', '"registrations"',
                       '"assign"', '"assignments"',
                       "--mountpoint", "--actor", "--class"):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)

    def test_parity_rows(self):
        matrix = read("docs/PARITY.md")
        for symbol in ("| /api/v1/storage/mounts |",
                       "| /api/v1/storage/registrations |",
                       "| /api/v1/storage/assignments |",
                       "`runStorageMounts`", "`runStorageRegister`",
                       "`runStorageAssign`"):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, matrix)


if __name__ == "__main__":
    unittest.main()
