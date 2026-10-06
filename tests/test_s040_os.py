"""S040 — OS Discovery conformance tests.

Guardrails for read-only host observation: the six goal facts come from
the documented sources, no new dependencies arrive, nothing is served yet
(S044 aggregates), and nothing writes. Parsing behavior and live values
are covered by Go tests in internal/discover.
"""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def read(rel: str) -> str:
    return (REPO / rel).read_text(encoding="utf-8")


class TestDiscoveryContract(unittest.TestCase):
    def test_six_facts_present(self):
        src = read("internal/discover/os.go")
        for field in ("DistroID", "DistroVersion", "Kernel", "Arch", "Hostname",
                      "MemoryBytes", "CPUCount", "CPUModel"):
            with self.subTest(field=field):
                self.assertIn(field, src)

    def test_documented_sources(self):
        src = read("internal/discover/os.go")
        for source in ("/etc/os-release", "/proc/sys/kernel/osrelease",
                       "/proc/meminfo", "/proc/cpuinfo", "os.Hostname()",
                       "runtime.GOARCH", "runtime.NumCPU()"):
            with self.subTest(source=source):
                self.assertIn(source, src)

    def test_linux_only_and_loud(self):
        src = read("internal/discover/os.go")
        self.assertIn("runtime.GOOS", src)
        self.assertIn("unsupported platform", src)

    def test_no_new_dependencies(self):
        import re

        direct = re.findall(r"(?m)^\t(\S+) v\S+$", read("go.mod"))
        self.assertNotIn("golang.org/x/sys", direct)

    def test_nothing_served_or_written(self):
        src = read("internal/discover/os.go")
        for banned in ("net/http", "HandleFunc", "os.WriteFile", "os.Create", "os.MkdirAll"):
            with self.subTest(banned=banned):
                self.assertNotIn(banned, src)


if __name__ == "__main__":
    unittest.main()
