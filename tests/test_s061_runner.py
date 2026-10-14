"""S061 — Operation Runner conformance tests.

Guardrails for background execution: definitions validate, Start
returns while steps run on detached contexts, failures stop fast with
pending steps untouched, panics fail instead of stranding, cancellation
lands, restarts recover stale records at boot — and nothing serves,
streams (S062), or audits (S063) here. Behavior is covered by Go tests
in internal/operation.
"""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def read(rel: str) -> str:
    return (REPO / rel).read_text(encoding="utf-8")


class TestRunnerContract(unittest.TestCase):
    def test_entry_points(self):
        src = read("internal/operation/runner.go")
        for symbol in ("type Runner struct", "type Definition struct",
                       "type StepFunc", "func NewRunner(",
                       "func (r *Runner) Start(", "func (r *Runner) Cancel(",
                       "func (r *Runner) Wait(", "func (r *Runner) Recover("):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)

    def test_detached_execution(self):
        src = read("internal/operation/runner.go")
        self.assertIn("WithoutCancel", src)
        self.assertIn("go r.execute(", src)

    def test_startup_recovery_wired(self):
        main = read("cmd/homestead-server/main.go")
        # S265: recovery keeps only the in-flight platform update's
        # operation, which the next start finalizes from its marker.
        self.assertTrue(".Recover()" in main or ".RecoverExcept(" in main)

    def test_no_serving_or_streaming(self):
        """Events are S062, audit S063, UI later still."""
        for rel in ("internal/operation/runner.go", "internal/operation/operation.go",
                    "internal/operation/store.go"):
            src = read(rel)
            for banned in ("net/http", "HandleFunc", "os/exec", "exec.Command",
                           "websocket", "sse", "text/event-stream"):
                with self.subTest(file=rel, banned=banned):
                    self.assertNotIn(banned, src)


if __name__ == "__main__":
    unittest.main()
