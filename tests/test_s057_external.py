"""S057 — External Firewall Mode conformance tests.

Guardrails for opting out: the mode lives in desired config (first real
domain schema, rendered by the existing config page and CLI with no new
routes), external managers are detected read-only, endpoints diagnose and
instruct instead of mutating when external, and the CLI plus wizard
surface the mode. ufw/firewalld service detection belongs here — S056
explicitly deferred it.
"""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent
CTL = REPO / "cmd" / "homesteadctl"


def read(rel: str) -> str:
    return (REPO / rel).read_text(encoding="utf-8")


def read_dash(rel: str) -> str:
    return (REPO / "dashboard" / rel).read_text(encoding="utf-8")


def ctl_sources() -> str:
    return "\n".join(
        p.read_text(encoding="utf-8") for p in CTL.glob("*.go") if not p.name.endswith("_test.go")
    )


class TestModeConfig(unittest.TestCase):
    def test_schema_field(self):
        src = read("internal/firewall/config.go")
        self.assertIn('"firewall_mode"', src)
        self.assertIn('"managed"', src)
        self.assertIn('"external"', src)
        self.assertIn("func Schema()", src)
        self.assertIn("func ModeOf(", src)

    def test_daemon_serves_domain_schema(self):
        main = read("cmd/homestead-server/main.go")
        self.assertIn("firewall.Schema()", main)
        self.assertNotIn("schema.Schema{}", main)


class TestExternalDetection(unittest.TestCase):
    def test_manager_probes(self):
        src = read("internal/firewall/detect.go")
        self.assertIn("ExternalManagers", src)
        self.assertIn('"ufw", "status"', src)
        self.assertIn('"firewall-cmd", "--state"', src)

    def test_endpoints_carry_mode(self):
        src = read("internal/server/firewall.go")
        self.assertIn("firewallMode(", src)
        self.assertIn("ModeExternal", src)
        self.assertIn("StatusConflict", src)
        self.assertIn('"external"', src)

    def test_no_new_routes(self):
        """Opt-out reuses status/plan/apply; the parity matrix is untouched."""
        src = read("internal/server/firewall.go")
        self.assertEqual(src.count("HandleFunc("), 3)


class TestSurfaces(unittest.TestCase):
    def test_cli_shows_mode(self):
        self.assertIn("mode:", ctl_sources())

    def test_wizard_relays_server_messages(self):
        src = read_dash("src/app/firewall/firewall.ts")
        self.assertIn("serverMessage", src)

    def test_dashboard_types(self):
        src = read_dash("src/app/core/api.service.ts")
        self.assertIn("mode: string", src)
        self.assertIn("external_managers", src)


if __name__ == "__main__":
    unittest.main()
