"""S000 — Product Constitution conformance tests.

Verifies that the product, ownership, UX, recovery, and network principles
from docs/PROJECT.md are present, internally consistent, and preserved by
docs/ARCHITECTURE.md, the ADRs, and the agent handoff rules.

Stdlib only (no third-party dependencies) so this slice does not preempt
the S002 repository bootstrap (Go modules / Angular / CI toolchain).
"""

import re
import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def read(rel: str) -> str:
    return (REPO / rel).read_text(encoding="utf-8")


class TestProductConstitution(unittest.TestCase):
    def test_project_constitution_sections_present(self):
        """PROJECT.md must state every constitution principle explicitly."""
        project = read("docs/PROJECT.md")
        for heading in (
            "## Product statement",
            "## Product promise",
            "## Ownership rule",
            "## UX rule",
            "## Network rule",
            "## Recovery rule",
        ):
            with self.subTest(heading=heading):
                self.assertIn(heading, project)

    def test_ownership_rule_forbids_lock_in(self):
        """Ownership rule must explicitly forbid runtime lock-in."""
        project = read("docs/PROJECT.md").lower()
        self.assertIn("runtime lock-in", project)
        self.assertIn("must not introduce runtime lock-in", project)
        self.assertIn("customer", project)

    def test_ux_rule_requires_cli_dashboard_parity(self):
        """UX rule must require homesteadctl + dashboard parity."""
        project = read("docs/PROJECT.md")
        self.assertIn("homesteadctl", project)
        self.assertIn("Angular", project)

    def test_recovery_rule_keeps_compose_usable(self):
        """Recovery rule must keep apps operable without Homestead."""
        project = read("docs/PROJECT.md")
        self.assertIn("Linux, Docker, and Compose", project)


class TestArchitecturePreservesConstitution(unittest.TestCase):
    def test_control_boundary(self):
        """Browser must never receive Docker socket access."""
        arch = read("docs/ARCHITECTURE.md")
        self.assertIn("never receives Docker socket access", arch)
        self.assertIn("homestead-server", arch)

    def test_desired_generated_observed_separation(self):
        arch = read("docs/ARCHITECTURE.md").lower()
        for state in ("desired state", "generated state", "observed state"):
            with self.subTest(state=state):
                self.assertIn(state, arch)

    def test_state_and_artifact_paths_declared(self):
        arch = read("docs/ARCHITECTURE.md")
        for path in ("/etc/homestead/", "/var/lib/homestead/"):
            with self.subTest(path=path):
                self.assertIn(path, arch)

    def test_sqlite_and_yaml_split(self):
        arch = read("docs/ARCHITECTURE.md")
        self.assertIn("SQLite", arch)
        self.assertIn("YAML", arch)


class TestAdrFrameworkIntact(unittest.TestCase):
    EXPECTED_ADRS = [f"ADR-{i:03d}" for i in range(1, 17)]

    def test_all_adrs_present_and_accepted(self):
        adr_dir = REPO / "docs" / "adr"
        for adr_id in self.EXPECTED_ADRS:
            with self.subTest(adr=adr_id):
                matches = list(adr_dir.glob(f"{adr_id}-*.md"))
                self.assertEqual(len(matches), 1, f"{adr_id} must have exactly one file")
                content = matches[0].read_text(encoding="utf-8")
                self.assertIn("## Status", content)
                self.assertIn("Accepted", content)

    def test_constitution_adrs_cover_all_principles(self):
        """Key boundary ADRs referenced by the constitution must exist."""
        adr_dir = REPO / "docs" / "adr"
        files = " ".join(p.name for p in adr_dir.glob("*.md"))
        for keyword in (
            "native-go-daemon",
            "cli-dashboard-parity",
            "compose-runtime",
            "local-first-exposure",
            "single-appliance",
            "secure-claim",
        ):
            with self.subTest(keyword=keyword):
                self.assertIn(keyword, files)


class TestDeferredLedgerConsistent(unittest.TestCase):
    def test_deferred_ledger_exists_and_defers_fleet(self):
        """Single-appliance scope requires fleet/clustering to stay deferred."""
        deferred = read("docs/DEFERRED.md").lower()
        self.assertIn("fleet management", deferred)
        self.assertIn("cluster", deferred)

    def test_deferred_does_not_claim_constitution_features(self):
        """Constitution-mandated features must not appear as deferred."""
        deferred = read("docs/DEFERRED.md").lower()
        for mandated in ("homestead-server", "homesteadctl", "angular dashboard"):
            with self.subTest(feature=mandated):
                self.assertNotIn(mandated, deferred)


class TestSliceHandoff(unittest.TestCase):
    def test_s000_slice_contract_exists(self):
        s000 = read("docs/slices/S000-product-constitution.md")
        self.assertIn("## Goal", s000)
        self.assertIn("## Acceptance Criteria", s000)
        self.assertRegex(s000, r"(?i)main build.*green|build.*green")

    def test_agent_handoff_preserves_ownership(self):
        agents = read("AGENTS.md")
        self.assertIn("no runtime lock-in", agents.lower())
        self.assertIn("homestead-server", agents)


if __name__ == "__main__":
    unittest.main()
