"""S234 — Disaster Recovery Acceptance conformance tests.

Guardrails for rebuilding from documented artifacts: export and
import routes serve the portable document with per-call secrets,
the CLI carries both over files, acceptance imports when an
export file is given and continues fresh otherwise, and backup
restoration from runs still awaits backup surfaces. Behavior is
covered by Go tests in internal/server and cmd/homesteadctl.
"""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def read(rel: str) -> str:
    return (REPO / rel).read_text(encoding="utf-8")


class TestDisasterRecovery(unittest.TestCase):
    def test_api_shapes(self):
        src = read("internal/server/configio.go")
        for symbol in ("GET /api/v1/config/export",
                       "POST /api/v1/config/import",
                       "func (s *Server) MountConfigIO("):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)

    def test_cli_shapes(self):
        src = read("cmd/homesteadctl/config.go")
        for symbol in ('"export"', '"import"',
                       '"secrets-file"', "runConfigExport",
                       "runConfigImport"):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)

    def test_acceptance_import_step(self):
        script = read("scripts/acceptance.sh")
        for symbol in ("HOMESTEAD_EXPORT_FILE", "HOMESTEAD_SECRETS_FILE",
                       "config import", "phase \"import\""):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, script)

    def test_parity_rows(self):
        matrix = read("docs/PARITY.md")
        for symbol in ("| /api/v1/config/export |",
                       "| /api/v1/config/import |",
                       '"export"', '"import"'):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, matrix)


if __name__ == "__main__":
    unittest.main()
