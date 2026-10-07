"""S094 — Route 53 DNS Provider conformance tests.

Guardrails for the Route 53 adapter: SigV4-signed calls against the
query API with an IAM key pair, exact zone resolution preferring public
zones over same-name private ones, paginated record-set listing expanded
to intent (multi-value sets per value, TXT unquoted, non-simple and
foreign sets skipped), transactional grouped apply where per-key final
sets compute from fresh state with unchanged sets skipped and emptied
sets deleted, TTL preferring created values then actual then explicit
default, and exact failures surfacing service errors. Behavior is
covered by Go tests in internal/dns against XML doubles.
"""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def read(rel: str) -> str:
    return (REPO / rel).read_text(encoding="utf-8")


class TestRoute53Contract(unittest.TestCase):
    def test_provider_shapes(self):
        src = read("internal/dns/route53.go")
        for symbol in ("func NewRoute53(", '"route53"', "CapabilityRecords",
                       "CapabilityDNS01", '"access_key_id"',
                       '"secret_access_key"', "func signV4(",
                       "AWS4-HMAC-SHA256"):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)

    def test_record_mapping(self):
        src = read("internal/dns/route53records.go")
        for symbol in ("func (r *Route53) ListRecords(",
                       "func (r *Route53) ApplyChanges(",
                       "ChangeResourceRecordSetsRequest",
                       "only as private", "unquoteTXT"):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)

    def test_provider_conformance(self):
        inner = read("internal/dns/route53_test.go")
        self.assertIn("var _ Provider = (*Route53)(nil)", inner)


if __name__ == "__main__":
    unittest.main()
