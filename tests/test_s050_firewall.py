"""S050 — nftables Detection conformance tests.

Guardrails for firewall observation: binary presence, version parsing,
JSON ruleset tables with the homestead-table flag, permission-denied
mapping, iptables backend flavor — read-only commands only, no new
dependencies, nothing served or written. Behavior is covered by Go
tests in internal/firewall with stubbed execution.
"""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def read(rel: str) -> str:
    return (REPO / rel).read_text(encoding="utf-8")


class TestDetectionContract(unittest.TestCase):
    def test_facts_present(self):
        src = read("internal/firewall/detect.go")
        for fact in ("NFTAvailable", "NFTVersion", "RulesetAccessible",
                     "Tables", "HasHomesteadTable", "IPTablesBackend"):
            with self.subTest(fact=fact):
                self.assertIn(fact, src)

    def test_read_only_commands(self):
        """Only list/version invocations; mutation arrives in later slices."""
        src = read("internal/firewall/detect.go")
        for allowed in ('"nft", "--version"', '"nft", "--json", "list", "ruleset"',
                        '"iptables", "--version"'):
            with self.subTest(allowed=allowed):
                self.assertIn(allowed, src)
        for banned in ("add", "flush", "delete", "create", "insert", "reset"):
            with self.subTest(banned=banned):
                self.assertNotIn(f'"{banned}"', src)

    def test_no_new_dependencies(self):
        """No netlink library: exec nft stays the interface (S002 owns the allowlist)."""
        import re

        direct = re.findall(r"(?m)^\t(\S+) v\S+$", read("go.mod"))
        self.assertNotIn("golang.org/x/sys", direct)

    def test_exec_scoped_to_firewall(self):
        """Process execution exists only here, never in discover."""
        self.assertIn("exec.Command", read("internal/firewall/detect.go"))
        for name in ("os.go", "hardware.go", "network.go", "storage.go"):
            with self.subTest(name=name):
                self.assertNotIn("exec.Command", read(f"internal/discover/{name}"))

    def test_nothing_served_or_written(self):
        src = read("internal/firewall/detect.go")
        for banned in ("net/http", "HandleFunc", "os.WriteFile", "os.Create"):
            with self.subTest(banned=banned):
                self.assertNotIn(banned, src)


if __name__ == "__main__":
    unittest.main()
