"""S042 — Network Discovery conformance tests.

Guardrails for network observation: interfaces with addresses, the IPv4
default gateway and its interface, and resolvers from the documented
sources; missing routes stay silent, nothing is served yet (S044
aggregates), and nothing writes. Parsing behavior and live values are
covered by Go tests in internal/discover.
"""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def read(rel: str) -> str:
    return (REPO / rel).read_text(encoding="utf-8")


class TestNetworkContract(unittest.TestCase):
    def test_facts_present(self):
        src = read("internal/discover/network.go")
        for fact in ("Interfaces", "DefaultGateway", "DefaultInterface",
                     "Resolvers", "HasInterface", "MTU", "MAC"):
            with self.subTest(fact=fact):
                self.assertIn(fact, src)

    def test_documented_sources(self):
        src = read("internal/discover/network.go")
        for source in ("/proc/net/route", "/etc/resolv.conf",
                       "net.Interfaces", "Addrs()"):
            with self.subTest(source=source):
                self.assertIn(source, src)

    def test_missing_gateway_silent(self):
        """Isolated hosts have no default route: empty fields, not errors."""
        src = read("internal/discover/network.go")
        self.assertIn("lowest-metric", src)

    def test_no_new_dependencies(self):
        import re

        direct = re.findall(r"(?m)^\t(\S+) v\S+$", read("go.mod"))
        self.assertNotIn("golang.org/x/sys", direct)
        self.assertNotIn("vishvananda", read("go.mod"))

    def test_nothing_served_or_written(self):
        src = read("internal/discover/network.go")
        for banned in ("net/http", "HandleFunc", "os.WriteFile", "os.Create", "exec.Command"):
            with self.subTest(banned=banned):
                self.assertNotIn(banned, src)


if __name__ == "__main__":
    unittest.main()
