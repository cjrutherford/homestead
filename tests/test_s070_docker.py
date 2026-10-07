"""S070 — Docker Detection conformance tests.

Guardrails for platform observation: Engine plus API versions and the
Compose v2 plugin version through the CLI, with legacy v1 flagged —
read-only invocations only, no new dependencies, nothing served or
written. Runtime health belongs to S073, installation to S071/S072.
Behavior is covered by Go tests in internal/docker with stubbed
execution.
"""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def read(rel: str) -> str:
    return (REPO / rel).read_text(encoding="utf-8")


class TestDockerContract(unittest.TestCase):
    def test_facts_present(self):
        src = read("internal/docker/docker.go")
        for fact in ("EngineAvailable", "EngineVersion", "APIVersion",
                     "ComposeAvailable", "ComposeVersion", "LegacyComposePresent"):
            with self.subTest(fact=fact):
                self.assertIn(fact, src)

    def test_read_only_invocations(self):
        """Only version queries; installation and lifecycle are later."""
        src = read("internal/docker/docker.go")
        for allowed in ('"docker", "version", "--format", "json"',
                        '"docker", "compose", "version", "--format", "json"'):
            with self.subTest(allowed=allowed):
                self.assertIn(allowed, src)
        for banned in ("pull", "push", "run", "up", "down", "exec", "rm ", "install"):
            with self.subTest(banned=banned):
                self.assertNotIn(f'"{banned}"', src)

    def test_no_new_dependencies(self):
        """CLI observation needs no SDK; the S002 allowlist is untouched."""
        self.assertNotIn("docker/docker", read("go.mod"))

    def test_nothing_served_or_written(self):
        src = read("internal/docker/docker.go")
        for banned in ("net/http", "HandleFunc", "os.WriteFile", "os.Create", "syscall."):
            with self.subTest(banned=banned):
                self.assertNotIn(banned, src)


if __name__ == "__main__":
    unittest.main()
