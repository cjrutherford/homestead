"""S489 - recreate a missing person with a generated password."""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def read(rel):
    return (REPO / rel).read_text(encoding="utf-8")


class RecreateGenerated(unittest.TestCase):
    def test_docs(self):
        self.assertIn("generated password", read("docs/slices/S489-recreate-generated-password.md"))
        self.assertIn("S489 offers", read("docs/SLICE_INDEX.md"))

    def test_button_sends_generated(self):
        html = read("dashboard/src/app/people/people.html")
        self.assertIn("resolve(person, 'recreate', 'generated')", html)
        self.assertIn("Recreate with a generated password", html)

    def test_parity_and_checklist(self):
        self.assertIn("--generate", read("docs/PARITY.md"))
        text = read("docs/reviews/OWNER_ACCEPTANCE_CHECKLIST.md")
        self.assertIn("**END-PARITY-7 ", text)
        self.assertIn("| END-PARITY | END-PARITY-1 to END-PARITY-7 (7) |", text)
        self.assertIn("END-PARITY-7", read("docs/reviews/acceptance-form.html"))


if __name__ == "__main__":
    unittest.main()
