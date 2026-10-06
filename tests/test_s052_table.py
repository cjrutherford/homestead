"""S052 — Dedicated Homestead Rules conformance tests.

Guardrails for table generation: deterministic policy expansion with
safe zone resolution, the isolated inet homestead table with an
accept-default base chain (never default-drop), and the management
baseline. Generation only — no apply, no serving, no new dependencies.
Behavior is covered by Go tests in internal/firewall, including a
skip-guarded nft --check golden test.
"""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def read(rel: str) -> str:
    return (REPO / rel).read_text(encoding="utf-8")


class TestTableGeneration(unittest.TestCase):
    def test_entry_points(self):
        src = read("internal/firewall/render.go")
        for symbol in ("func Expand(", "func Render(", "func LocalSubnets(",
                       "func ManagementBaseline("):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)

    def test_isolated_table(self):
        src = read("internal/firewall/render.go")
        self.assertIn("TableFamily", src)
        self.assertIn('"inet"', src)
        self.assertIn("HomesteadTable", src)
        self.assertIn("policy accept", src)
        self.assertNotIn("policy drop", src)

    def test_safe_expansion(self):
        src = read("internal/firewall/render.go")
        self.assertIn("has no addresses", src)
        self.assertIn("S110", src)

    def test_generation_only(self):
        """Apply is S054, planning is S053, serving is later still."""
        src = read("internal/firewall/render.go")
        for banned in ("exec.Command", "net/http", "HandleFunc", "os.WriteFile"):
            with self.subTest(banned=banned):
                self.assertNotIn(banned, src)
        self.assertNotIn("homestead/internal", src.replace(
            '"homestead/internal/discover"', ""))


if __name__ == "__main__":
    unittest.main()
