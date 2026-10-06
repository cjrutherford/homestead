"""S051 — Firewall Domain Model conformance tests.

Guardrails for the firewall vocabulary: zones, services, sources, rules,
policies, and ordered sets with validation — pure data, no rendering
(S053), no apply (S054), no serving, no new dependencies. Behavior is
covered by Go tests in internal/firewall.
"""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def read(rel: str) -> str:
    return (REPO / rel).read_text(encoding="utf-8")


class TestModelContract(unittest.TestCase):
    def test_vocabulary_present(self):
        src = read("internal/firewall/model.go")
        for symbol in ("type Zone", "type Service", "type Source", "type Action",
                       "type Rule struct", "type RuleSet", "type ServicePolicy",
                       "func (s RuleSet) Sorted("):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)

    def test_zones_match_exposure_language(self):
        src = read("internal/firewall/model.go")
        for zone in ('ZoneLocal Zone = "local"', 'ZoneOverlay Zone = "overlay"',
                     'ZonePublic Zone = "public"'):
            with self.subTest(zone=zone):
                self.assertIn(zone, src)

    def test_validation_entry_points(self):
        src = read("internal/firewall/model.go")
        for method in ("func (s Service) Validate(", "func (s Source) Validate(",
                       "func (r Rule) Validate(", "func (s RuleSet) Validate(",
                       "func (p ServicePolicy) Validate("):
            with self.subTest(method=method):
                self.assertIn(method, src)

    def test_model_stays_pure(self):
        """Rendering is S053, apply is S054, serving is later still."""
        src = read("internal/firewall/model.go")
        for banned in ("net/http", "HandleFunc", "os/exec", "exec.Command",
                       "os.WriteFile", "nft ", '"add"', '"flush"'):
            with self.subTest(banned=banned):
                self.assertNotIn(banned, src)
        self.assertNotIn("homestead/internal", src)


if __name__ == "__main__":
    unittest.main()
