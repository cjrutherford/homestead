"""S075 — Stack Lifecycle conformance tests.

Guardrails for runner-backed lifecycle: pull/up/stop/restart as single
compose invocations plus two-step redeploy, all through explicit project
flags and all recorded as operations — with removal deliberately absent
(S158 owns it) and surfacing deferred to catalog operations (S156+).
Behavior is covered by Go tests in internal/docker with stubbed
execution driving the real runner.
"""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def read(rel: str) -> str:
    return (REPO / rel).read_text(encoding="utf-8")


class TestLifecycleContract(unittest.TestCase):
    def test_definition_builders(self):
        src = read("internal/docker/lifecycle.go")
        for symbol in ("func PullDefinition(", "func StopDefinition("):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)
        # Verbs that start containers live behind the craft-gated catalog
        # paths (S249); an ungated definition here would bypass the gate.
        for banned in ("func UpDefinition(", "func RestartDefinition(",
                       "func RedeployDefinition("):
            with self.subTest(banned=banned):
                self.assertNotIn(banned, src)

    def test_compose_invocation(self):
        src = read("internal/docker/lifecycle.go")
        self.assertIn('"compose"', src)
        self.assertIn('"--project-directory"', src)
        self.assertIn('"--project-name"', src)

    def test_no_removal(self):
        """Stopping keeps containers; S158 owns removal (docs may say so)."""
        src = read("internal/docker/lifecycle.go")
        for banned in ('"down"', '"rm"'):
            with self.subTest(banned=banned):
                self.assertNotIn(banned, src)

    def test_no_serving(self):
        src = read("internal/docker/lifecycle.go")
        for banned in ("net/http", "HandleFunc"):
            with self.subTest(banned=banned):
                self.assertNotIn(banned, src)


if __name__ == "__main__":
    unittest.main()
