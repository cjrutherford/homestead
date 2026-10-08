"""S162 — Jellyfin Hardware Acceleration conformance tests.

Guardrails for transcode detection: vendor stacks need both the
device and its usable node with half stacks falling to software,
discrete NVIDIA preferred over integrated Intel over AMD, devices
passed through with runtimes and environment where the stack needs
them, group additions deliberately absent, and ARM SoC paths out of
scope. Behavior is covered by Go tests in internal/jellyfin
against fixture hardware.
"""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def read(rel: str) -> str:
    return (REPO / rel).read_text(encoding="utf-8")


class TestAccelContract(unittest.TestCase):
    def test_accel_shapes(self):
        src = read("internal/jellyfin/accel.go")
        for symbol in ("func DetectAcceleration(", "type Acceleration",
                       '"qsv"', '"vaapi"', '"nvenc"', '"none"',
                       "NVIDIA_VISIBLE_DEVICES"):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)


if __name__ == "__main__":
    unittest.main()
