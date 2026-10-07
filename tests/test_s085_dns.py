"""S085 — Local DNS Provider conformance tests.

Guardrails for the AdGuard Home adapter: a client over the published
web origin with basic auth, serving proof through the control status
endpoint, custom DNS answers managed as validated rewrites (canonical
lowercase domains, IP answers), tolerant decoding against API evolution,
fast failure on unreachable or refusing instances, and no serving of
its own. Behavior is covered by Go tests in internal/dns against
httptest doubles; install wizard automation stays out (see DEFERRED).
"""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def read(rel: str) -> str:
    return (REPO / rel).read_text(encoding="utf-8")


class TestDNSContract(unittest.TestCase):
    def test_adapter_shapes(self):
        src = read("internal/dns/adguard.go")
        for symbol in ("type Client", "func NewAdGuard(", "type Status",
                       "type Rewrite", "func (c *Client) FetchStatus(",
                       "func (c *Client) ListRewrites(",
                       "func (c *Client) AddRewrite(",
                       "func (c *Client) DeleteRewrite("):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)

    def test_control_endpoints(self):
        src = read("internal/dns/adguard.go")
        for symbol in ('"/control/status"', '"/control/rewrite/list"',
                       '"/control/rewrite/add"', '"/control/rewrite/delete"',
                       "SetBasicAuth"):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)

    def test_validated_rewrites(self):
        src = read("internal/dns/adguard.go")
        self.assertIn("must be lowercase", src)
        self.assertIn("ParseAddr", src)

    def test_client_only(self):
        src = read("internal/dns/adguard.go")
        for banned in ("HandleFunc", "ListenAndServe"):
            with self.subTest(banned=banned):
                self.assertNotIn(banned, src)


if __name__ == "__main__":
    unittest.main()
