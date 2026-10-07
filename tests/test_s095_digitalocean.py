"""S095 — DigitalOcean DNS Provider conformance tests.

Guardrails for the DigitalOcean adapter: bearer-token auth against the
v2 JSON API, exact zone existence, paginated record listing normalized
to intent (relative names direct, CNAME targets trimmed, foreign types
skipped), ordered apply with creates posting (TTL-less records leaving
the field to the API default, CNAME absolute) and deletes resolving
through a fresh listing to record IDs, exact failures for missing
delete targets and API errors, and no state beyond the call. Behavior
is covered by Go tests in internal/dns against httptest doubles.
"""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def read(rel: str) -> str:
    return (REPO / rel).read_text(encoding="utf-8")


class TestDigitalOceanContract(unittest.TestCase):
    def test_provider_shapes(self):
        src = read("internal/dns/digitalocean.go")
        for symbol in ("func NewDigitalOcean(", '"digitalocean"',
                       "CapabilityRecords", "CapabilityDNS01",
                       '"api_token"',
                       "func (d *DigitalOcean) ListRecords(",
                       "func (d *DigitalOcean) ApplyChanges("):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)

    def test_api_mapping(self):
        src = read("internal/dns/digitalocean.go")
        for symbol in ("Bearer ", "/domains", "/records", "links",
                       "domain_records", "delete target not found"):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)

    def test_provider_conformance(self):
        inner = read("internal/dns/digitalocean_test.go")
        self.assertIn("var _ Provider = (*DigitalOcean)(nil)", inner)


if __name__ == "__main__":
    unittest.main()
