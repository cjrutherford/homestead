"""S093 — Cloudflare DNS Provider conformance tests.

Guardrails for the Cloudflare adapter: bearer-token auth against the v4
API, exact-single zone resolution, paginated listing normalized to
intent (FQDN to relative names, automatic TTL to default, foreign
types and foreign zones skipped), ordered apply with creates posting
and deletes resolving through a fresh listing to provider IDs, exact
failures for missing delete targets and API errors, and no state beyond
the call. Behavior is covered by Go tests in internal/dns against
httptest doubles.
"""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def read(rel: str) -> str:
    return (REPO / rel).read_text(encoding="utf-8")


class TestCloudflareContract(unittest.TestCase):
    def test_provider_shapes(self):
        src = read("internal/dns/cloudflare.go")
        for symbol in ("func NewCloudflare(", '"cloudflare"',
                       "CapabilityRecords", "CapabilityDNS01",
                       '"api_token"', "func (c *Cloudflare) ListRecords(",
                       "func (c *Cloudflare) ApplyChanges("):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)

    def test_api_mapping(self):
        src = read("internal/dns/cloudflare.go")
        for symbol in ("Bearer ", "/zones", "/dns_records", "result_info",
                       "total_pages", "delete target not found"):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)

    def test_provider_conformance(self):
        inner = read("internal/dns/cloudflare_test.go")
        self.assertIn("var _ Provider = (*Cloudflare)(nil)", inner)


if __name__ == "__main__":
    unittest.main()
