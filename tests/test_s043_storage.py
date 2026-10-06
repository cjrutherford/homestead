"""S043 — Mounted Storage Discovery conformance tests.

Guardrails for mount observation: every mount with usage and stable
UUID/label identity from the documented sources, stale mounts marked
rather than fatal, longest-prefix lookup for later slices — and absolute
restraint: nothing formats, partitions, writes, or serves (S140+ owns
selection; the doctor aggregates in S044). Parsing behavior and live
values are covered by Go tests in internal/discover.
"""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def read(rel: str) -> str:
    return (REPO / rel).read_text(encoding="utf-8")


class TestStorageContract(unittest.TestCase):
    def test_facts_present(self):
        src = read("internal/discover/storage.go")
        for fact in ("Mounts", "UUID", "Label", "FreeBytes", "AvailBytes",
                     "TotalBytes", "Accessible", "FindMount", "ReadOnly"):
            with self.subTest(fact=fact):
                self.assertIn(fact, src)

    def test_documented_sources(self):
        src = read("internal/discover/storage.go")
        for source in ("/proc/self/mountinfo", "Statfs", "/dev/disk/by-uuid",
                       "/dev/disk/by-label"):
            with self.subTest(source=source):
                self.assertIn(source, src)

    def test_stale_mounts_marked_not_fatal(self):
        src = read("internal/discover/storage.go")
        self.assertIn("Accessible", src)

    def test_no_new_dependencies(self):
        import re

        direct = re.findall(r"(?m)^\t(\S+) v\S+$", read("go.mod"))
        self.assertNotIn("golang.org/x/sys", direct)

    def test_nothing_served_or_mutated(self):
        """ADR-013: discovery never formats, partitions, writes, or serves."""
        src = read("internal/discover/storage.go")
        for banned in ("net/http", "HandleFunc", "os.WriteFile", "os.Create",
                       "os.MkdirAll", "os.Remove", "exec.Command", "mkfs", "fdisk"):
            with self.subTest(banned=banned):
                self.assertNotIn(banned, src)


if __name__ == "__main__":
    unittest.main()
