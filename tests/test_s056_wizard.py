"""S056 — Firewall Wizard conformance tests.

Guardrails for guided firewall setup: LAN identification, recommended
rules, review, and guarded apply across the daemon API, the CLI, and the
dashboard wizard — with the management baseline protecting loopback,
the ufw/firewalld world left to S057, and no new dependencies. Behavior
is covered by Go tests, Vitest specs, and the S037 parity matrix (which
gains the three firewall rows here).
"""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent
CTL = REPO / "cmd" / "homesteadctl"
APP = REPO / "dashboard" / "src" / "app"


def read(rel: str) -> str:
    return (REPO / rel).read_text(encoding="utf-8")


def read_dash(rel: str) -> str:
    return (REPO / "dashboard" / rel).read_text(encoding="utf-8")


def ctl_sources() -> str:
    return "\n".join(
        p.read_text(encoding="utf-8") for p in CTL.glob("*.go") if not p.name.endswith("_test.go")
    )


class TestBaselineLoopback(unittest.TestCase):
    def test_loopback_accepts(self):
        """Local tools verify through the guard with unknown interfaces."""
        src = read("internal/firewall/render.go")
        self.assertIn("management-loopback", src)
        self.assertIn("127.0.0.0/8", src)
        self.assertIn("::1/128", src)


class TestFirewallApi(unittest.TestCase):
    def test_endpoints(self):
        src = read("internal/server/firewall.go")
        for route in ("GET /api/v1/firewall/status", "GET /api/v1/firewall/plan",
                      "POST /api/v1/firewall/apply"):
            with self.subTest(route=route):
                self.assertIn(route, src)

    def test_guard_uses_caller(self):
        src = read("internal/server/firewall.go")
        self.assertIn("requestConnection", src)
        self.assertIn("127.0.0.1", src)
        self.assertIn("StatusConflict", src)

    def test_daemon_port_wired(self):
        main = read("cmd/homestead-server/main.go")
        self.assertIn("MountFirewall", read("internal/server/mount.go"))
        self.assertIn("daemonPort", main)


class TestFirewallCli(unittest.TestCase):
    def test_commands(self):
        src = ctl_sources()
        for cmd in ("runFirewallStatus", "runFirewallPlan", "runFirewallApply", '"firewall"'):
            with self.subTest(cmd=cmd):
                self.assertIn(cmd, src)


class TestFirewallWizard(unittest.TestCase):
    def test_page_files_exist(self):
        for name in ("firewall/firewall.ts", "firewall/firewall.html",
                     "firewall/firewall.spec.ts"):
            with self.subTest(name=name):
                self.assertTrue((APP / name).is_file())

    def test_wizard_framework_use(self):
        src = read_dash("src/app/firewall/firewall.ts")
        self.assertIn("WizardShell", src)
        self.assertIn("WizardService", src)
        self.assertIn("'firewall'", src)

    def test_flow_markers(self):
        html = read_dash("src/app/firewall/firewall.html")
        self.assertIn("local_subnets", html)
        self.assertIn("Confirm apply", html)
        src = read_dash("src/app/firewall/firewall.ts")
        self.assertIn("409", src)

    def test_route_and_nav(self):
        self.assertIn("path: 'firewall'", read_dash("src/app/app.routes.ts"))
        self.assertIn("path: '/firewall'", read_dash("src/app/core/nav.ts"))

    def test_no_external_firewall_logic(self):
        """S057 owns external-firewall mode; ufw/firewalld stay out."""
        import re

        for rel in ("internal/server/firewall.go", "internal/firewall/render.go"):
            code = re.sub(r"(?m)^\s*//.*$", "", read(rel).lower())
            for banned in (r"\bufw\b", r"\bfirewalld\b", "firewall-cmd"):
                with self.subTest(file=rel, banned=banned):
                    self.assertIsNone(re.search(banned, code))


if __name__ == "__main__":
    unittest.main()
