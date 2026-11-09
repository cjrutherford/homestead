"""S485 - daemon version from GET /version on the Status page."""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def read(rel):
    return (REPO / rel).read_text(encoding="utf-8")


class VersionOnDashboard(unittest.TestCase):
    def test_docs(self):
        self.assertIn("daemonVersion", read("docs/slices/S485-version-on-dashboard.md"))
        self.assertIn("S485 shows the daemon version", read("docs/SLICE_INDEX.md"))

    def test_wrapper_and_page(self):
        self.assertIn("'/version'", read("dashboard/src/app/core/api.service.ts"))
        self.assertIn("this.api.daemonVersion()", read("dashboard/src/app/status/status.ts"))
        self.assertIn("daemonVersion()", read("dashboard/src/app/status/status.html"))

    def test_parity_row(self):
        text = read("docs/PARITY.md")
        self.assertIn("| GET | /version | `\"version\"` | `daemonVersion` |", text)

    def test_checklist(self):
        text = read("docs/reviews/OWNER_ACCEPTANCE_CHECKLIST.md")
        self.assertIn("**END-PARITY-5 ", text)
        self.assertIn("| END-PARITY | END-PARITY-1 to END-PARITY-7 (7) |", text)
        self.assertIn("END-PARITY-5", read("docs/reviews/acceptance-form.html"))


if __name__ == "__main__":
    unittest.main()
