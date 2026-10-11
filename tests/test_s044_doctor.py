"""S044 — System Doctor conformance tests.

Guardrails for explainable diagnostics: thresholded checks with stable
names over the S040-S043 facts, served on one endpoint, consumed by the
Go client, the CLI (exit 1 on failure), and the status page. Thresholds
are S044 baselines, documented as tunable. Behavior is covered by Go
tests in internal/doctor, internal/server, internal/apiclient, and
cmd/homesteadctl plus Vitest specs run via make dashboard-test.
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


class TestDoctorContract(unittest.TestCase):
    def test_checks_have_stable_names(self):
        src = read("internal/doctor/doctor.go")
        for name in ("os-distro", "memory", "cpu", "default-gateway",
                     "dns-resolvers", "root-disk", "stale-mounts", "state-dirs"):
            with self.subTest(name=name):
                self.assertIn(f'"{name}"', src)

    def test_severity_ordering(self):
        src = read("internal/doctor/doctor.go")
        self.assertIn("StatusOK", src)
        self.assertIn("StatusWarn", src)
        self.assertIn("StatusFail", src)
        self.assertIn("func overall(", src)

    def test_endpoint_and_client(self):
        self.assertIn("GET /api/v1/doctor", read("internal/server/doctor.go"))
        self.assertIn("MountDoctor", read("internal/server/mount.go"))
        self.assertIn("func (c *Client) Doctor(", read("internal/apiclient/doctor.go"))

    def test_cli_command(self):
        src = ctl_sources()
        self.assertIn('"doctor"', src)
        self.assertIn("overall: ", src)

    def test_dashboard_surface(self):
        self.assertIn("'/api/v1/doctor'", read_dash("src/app/core/api.service.ts"))
        self.assertIn("Diagnostics", read_dash("src/app/status/status.html"))

    def test_parity_row(self):
        parity = read("docs/PARITY.md")
        self.assertIn("| GET | /api/v1/doctor |", parity)
        self.assertIn("`doctor`", parity)


if __name__ == "__main__":
    unittest.main()
