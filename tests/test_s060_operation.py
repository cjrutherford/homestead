"""S060 — Operation Domain conformance tests.

Guardrails for operation records: typed operations with a strict state
machine, ordered steps, validation, and versioned storage (migration v5
extends the S013 skeleton). Execution belongs to the runner (S061),
events to S062, audit to S063 — this slice defines, validates,
transitions, and persists. Behavior is covered by Go tests in
internal/operation.
"""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def read(rel: str) -> str:
    return (REPO / rel).read_text(encoding="utf-8")


class TestOperationContract(unittest.TestCase):
    def test_model_present(self):
        src = read("internal/operation/operation.go")
        for symbol in ("type Operation struct", "type Step struct",
                       "func CanTransition(", "func Terminal(",
                       "func ValidateType(", "func ValidatePayload(",
                       "StatusPending", "StatusRunning", "StatusSucceeded",
                       "StatusFailed", "StatusCancelled"):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)

    def test_store_present(self):
        src = read("internal/operation/store.go")
        for symbol in ("func New(", "func (s *Store) Create(",
                       "func (s *Store) Get(", "func (s *Store) List(",
                       "func (s *Store) Transition(",
                       "func (s *Store) AddStep(",
                       "func (s *Store) TransitionStep(",
                       "ErrNotFound", "ErrBadTransition"):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)

    def test_migration_v5(self):
        import re

        src = read("internal/store/store.go")
        version = int(re.search(r"(?m)^const CurrentVersion = (\d+)$", src).group(1))
        self.assertGreaterEqual(version, 5)
        self.assertIn("CREATE TABLE IF NOT EXISTS operation_steps", src)
        self.assertIn("ALTER TABLE operations ADD COLUMN type", src)

    def test_no_execution_or_serving(self):
        """Runner is S061, events S062, UI later still."""
        for rel in ("internal/operation/operation.go", "internal/operation/store.go"):
            src = read(rel)
            for banned in ("net/http", "HandleFunc", "os/exec", "exec.Command",
                           "goroutine", "go func"):
                with self.subTest(file=rel, banned=banned):
                    self.assertNotIn(banned, src)


if __name__ == "__main__":
    unittest.main()
