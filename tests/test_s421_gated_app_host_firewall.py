"""S421 - the host firewall for gated apps (ADR-055 section 5 amendment).

The rules and the reconcile are covered by the Go tests. This pins the facts
that must not drift silently: the rule shape in the source, the docs, and that
no manifest requires the block before S422 does it deliberately.
"""
import unittest
from pathlib import Path

import yaml

REPO = Path(__file__).resolve().parent.parent


def read(rel):
    return (REPO / rel).read_text()


class TestHostBlock(unittest.TestCase):
    def test_rules_in_source(self):
        src = read("internal/firewall/gate.go")
        for want in (
            'GateTable = "homestead_gate"',
            "ct status dnat accept",
            "meta skuid != 0 drop",
            'oifname %q ct status dnat accept',
            'oifname %q meta skuid != 0 drop',
            'iifname != \\"docker0\\" iifname != \\"br-*\\" ct state new drop',
            '"br-*"',
            "0x0*20",
            "type filter hook output",
            "type filter hook forward",
        ):
            self.assertIn(want, src)

    def test_interface_based_not_subnet_based(self):
        src = read("internal/firewall/gate.go")
        self.assertNotIn("daddr", src)
        self.assertNotIn("netip", src)
        self.assertNotIn("edgeSubnet", read("internal/server/gateblock.go"))

    def test_only_the_gate_table_is_touched(self):
        src = read("internal/firewall/gate.go")
        self.assertNotIn("HomesteadTable", src)
        self.assertNotIn("flush ruleset", src)

    def test_reconciled_and_started(self):
        self.assertIn("reconcileGateBlock", read("internal/server/tunnel.go"))
        self.assertIn("reconcileGateBlock", read("internal/server/forwardauth.go"))
        self.assertIn("RunGateBlock", read("cmd/homestead-server/main.go"))
        self.assertIn("gated-app-host-block", read("internal/server/doctorlive.go"))

    def test_no_manifest_requires_it_yet(self):
        for path in (REPO / "catalog").glob("*.yaml"):
            fa = (yaml.safe_load(path.read_text()).get("sso") or {}).get("forwardauth") or {}
            self.assertNotIn("requireshostblock", fa, path.name)

    def test_docs(self):
        adr = read("docs/adr/ADR-055-forward-auth-for-navidrome-and-uptime-kuma.md")
        self.assertIn("S421", adr)
        self.assertIn("homestead_gate", adr)
        self.assertIn("Accepted", adr.split("## Status")[1].splitlines()[1])
        status = read("docs/adr/ADR-008-firewall-boundary.md").split("## Status")[1].splitlines()[1]
        self.assertEqual(status, "Accepted")
        self.assertTrue((REPO / "docs/slices/S421-gated-app-host-firewall.md").exists())
        for doc in ("docs/DEFERRED.md", "docs/SLICE_INDEX.md"):
            self.assertIn("S421", read(doc))


if __name__ == "__main__":
    unittest.main()
