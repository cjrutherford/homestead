"""S090 — DNS Intent Model conformance tests.

Guardrails for provider-neutral DNS intent: zones with baseline
records, per-audience views (local/overlay/public) carrying the answers
each origin sees, A/AAAA/CNAME/TXT records with per-type values and
bounded TTLs, relative names with underscore labels allowed for
challenge names, CNAME exclusivity, duplicate rejection with round-robin
repeats legal, one view per audience, and deterministic ordering. The
model validates shape only — merging and provider rendering belong to
later slices. Behavior is covered by Go tests in internal/dns.
"""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def read(rel: str) -> str:
    return (REPO / rel).read_text(encoding="utf-8")


class TestIntentContract(unittest.TestCase):
    def test_intent_shapes(self):
        src = read("internal/dns/model.go")
        for symbol in ("type Audience", "type RecordType", "type Record",
                       "type View", "type Zone", "func (r Record) Validate()",
                       "func (v View) Validate()", "func (z Zone) Validate()",
                       "func (z Zone) Sorted()"):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)

    def test_shared_vocabulary(self):
        src = read("internal/dns/model.go")
        for symbol in ('"local"', '"overlay"', '"public"', '"A"',
                       '"AAAA"', '"CNAME"', '"TXT"',
                       "duplicate view audience", "excludes other records",
                       "duplicate record"):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)

    def test_provider_neutral(self):
        src = read("internal/dns/model.go")
        for banned in ("adguard", "cloudflare", "route53", "digitalocean",
                       "net/http", "HandleFunc"):
            with self.subTest(banned=banned):
                self.assertNotIn(banned.lower(), src.lower())


if __name__ == "__main__":
    unittest.main()
