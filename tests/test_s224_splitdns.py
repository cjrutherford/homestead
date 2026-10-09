"""S224 — Split DNS Bootstrap conformance tests.

Guardrails for reaching SPLIT_DNS_READY without going public: the
zone intent carries a local view to the appliance LAN address with
no public view, local answers ensure in local DNS without deleting
foreign rewrites, the public audience previews with changes held
and applies nothing, API-less providers skip remote proof
explicitly, re-runs converge, refusals record nothing, and the
stage records one row for later bootstrap slices. Behavior is
covered by Go tests in internal/dns over schema v17.
"""

import re
import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def read(rel: str) -> str:
    return (REPO / rel).read_text(encoding="utf-8")


class TestSplitDNSBootstrap(unittest.TestCase):
    def test_bootstrap_shapes(self):
        src = read("internal/dns/bootstrap.go")
        for symbol in ("type SplitDNS", "func EstablishSplitDNS(",
                       "func CurrentSplitDNS(", "ErrNoSplitDNS",
                       "AudienceLocal", "PublicVerified",
                       "would go public"):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)

    def test_non_public_by_construction(self):
        src = read("internal/dns/bootstrap.go")
        self.assertNotIn("ApplyChanges(", src)
        self.assertIn("previews only", src)

    def test_schema_v17(self):
        src = read("internal/store/store.go")
        version = int(re.search(r"(?m)^const CurrentVersion = (\d+)$", src).group(1))
        self.assertGreaterEqual(version, 17)
        self.assertIn("CREATE TABLE IF NOT EXISTS split_dns_state", src)


if __name__ == "__main__":
    unittest.main()
