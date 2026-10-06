"""S041 — Hardware Discovery conformance tests.

Guardrails for hardware observation: machine identity, CPU capability
flags, and GPU devices from sysfs/devfs; missing optional sources stay
silent, nothing is served yet (S044 aggregates), and nothing writes.
Parsing behavior and live values are covered by Go tests in
internal/discover.
"""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def read(rel: str) -> str:
    return (REPO / rel).read_text(encoding="utf-8")


class TestHardwareContract(unittest.TestCase):
    def test_facts_present(self):
        src = read("internal/discover/hardware.go")
        for fact in ("MachineVendor", "MachineModel", "CPUFlags", "GPUs",
                     "HasFlag", "VendorID", "DeviceID", "Driver"):
            with self.subTest(fact=fact):
                self.assertIn(fact, src)

    def test_gpu_kinds(self):
        src = read("internal/discover/hardware.go")
        for kind in ("drm-render", "drm-card", "nvidia", "pci-vga", "pci-3d"):
            with self.subTest(kind=kind):
                self.assertIn(kind, src)

    def test_documented_sources(self):
        src = read("internal/discover/hardware.go")
        for source in ("/sys", "/dev", "bus/pci/devices", "devices/virtual/dmi/id",
                       "cpuinfo", "flags"):
            with self.subTest(source=source):
                self.assertIn(source, src)

    def test_missing_sources_silent(self):
        """Containers often lack DMI or /dev/dri: empty values, not errors."""
        src = read("internal/discover/hardware.go")
        self.assertIn("optional", src.lower())

    def test_no_new_dependencies(self):
        import re

        direct = re.findall(r"(?m)^\t(\S+) v\S+$", read("go.mod"))
        self.assertNotIn("golang.org/x/sys", direct)

    def test_nothing_served_or_written(self):
        src = read("internal/discover/hardware.go")
        for banned in ("net/http", "HandleFunc", "os.WriteFile", "os.Create", "os.MkdirAll"):
            with self.subTest(banned=banned):
                self.assertNotIn(banned, src)


if __name__ == "__main__":
    unittest.main()
