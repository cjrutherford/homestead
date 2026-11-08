"""S482 - live acceptance of the subpath work: docs and checklist rows exist."""

import re
import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def read(rel):
    return (REPO / rel).read_text(encoding="utf-8")


class SubpathAcceptance(unittest.TestCase):
    def test_slice_doc_and_index(self):
        self.assertIn("END-SUBPATH-1", read("docs/slices/S482-subpath-live-acceptance.md"))
        self.assertIn("S482 is the live acceptance", read("docs/SLICE_INDEX.md"))

    def test_checklist_rows(self):
        text = read("docs/reviews/OWNER_ACCEPTANCE_CHECKLIST.md")
        for n in range(1, 9):
            self.assertIn(f"**END-SUBPATH-{n} ", text)
        self.assertIn("| END-SUBPATH | END-SUBPATH-1 to END-SUBPATH-8 (8) |", text)
        self.assertIn("END-SUBPATH-1", read("docs/reviews/acceptance-form.html"))

    def test_deferred(self):
        self.assertTrue(re.search(r"S482: FreshRSS sign-in fails for the owner", read("docs/DEFERRED.md")))


if __name__ == "__main__":
    unittest.main()
