"""S221 — First-run Local Discovery conformance tests.

Guardrails for LAN discoverability without public prerequisites:
the daemon answers homestead.local A records plus a service
advertisement carrying live claim state, scoped to IPv4
multicast-capable LAN interfaces with no probing (single
appliance per LAN by design), startup conflicts failing loudly
instead of silently undiscoverable, and discovery never failing
the boot. Behavior is covered by Go tests in internal/mdns
including a loopback socket round-trip.
"""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def read(rel: str) -> str:
    return (REPO / rel).read_text(encoding="utf-8")


class TestLocalDiscovery(unittest.TestCase):
    def test_responder_shapes(self):
        src = read("internal/mdns/mdns.go") + read("internal/mdns/serve.go")
        for symbol in ("type Config", "func (c Config) Validate(",
                       "func LANIPv4(", "func parseQuestions(",
                       "func ListenAndServe("):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)

    def test_answers_hostname_and_service(self):
        src = read("internal/mdns/mdns.go")
        for symbol in ('".local"', '"_homestead._tcp.local"',
                       '"state="', "func announce("):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)

    def test_daemon_wiring(self):
        main = read("cmd/homestead-server/main.go")
        for symbol in ("startLocalDiscovery", "mdns.ListenAndServe",
                       "local discovery off:"):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, main)

    def test_no_new_dependencies(self):
        modem = read("go.mod")
        self.assertNotIn("miekg/dns", modem)
        self.assertNotIn("mdns", modem)


if __name__ == "__main__":
    unittest.main()
