"""S183 — Backup Scheduler conformance tests.

Guardrails for retained jobs: schedules with hourly floors and
non-negative retention persisted per package, job definitions with
file copies plus database exports plus timestamp-gated pruning plus
closing manifests, single timestamps per run, retention keeping
newest with foreign files surviving, and results recording through
the operation engine. Wall-clock triggering belongs to timers.
Behavior is covered by Go tests in internal/backup against
temporary filesystems.
"""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def read(rel: str) -> str:
    return (REPO / rel).read_text(encoding="utf-8")


class TestScheduleContract(unittest.TestCase):
    def test_schedule_shapes(self):
        src = read("internal/backup/schedule.go")
        for symbol in ("type Schedule", "func (s *Schedules) Save(",
                       "func JobDefinition(", "type Job",
                       '"backup.job"', "backup_schedules"):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)

    def test_persisted(self):
        import re
        migration = read("internal/store/store.go")
        self.assertIn("backup_schedules", migration)
        version = int(re.search(r"(?m)^const CurrentVersion = (\d+)$", migration).group(1))
        self.assertGreaterEqual(version, 11)


if __name__ == "__main__":
    unittest.main()
