"""S487 - craft version and signature inputs on the Updates page."""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def read(rel):
    return (REPO / rel).read_text(encoding="utf-8")


class CraftVersionSignature(unittest.TestCase):
    def test_docs(self):
        self.assertIn("craft-version", read("docs/slices/S487-craft-version-signature.md"))
        self.assertIn("S487 adds a Version input", read("docs/SLICE_INDEX.md"))

    def test_version_input(self):
        self.assertIn('name="craft-version"', read("dashboard/src/app/updates/craft-images.html"))
        self.assertIn("version: this.version || undefined", read("dashboard/src/app/updates/craft-images.ts"))

    def test_signature_input(self):
        self.assertIn("'craft-signature-'", read("dashboard/src/app/updates/updates.html"))
        self.assertIn("'signature'", read("dashboard/src/app/updates/updates.ts"))

    def test_parity_and_checklist(self):
        self.assertIn("--version", read("docs/PARITY.md"))
        text = read("docs/reviews/OWNER_ACCEPTANCE_CHECKLIST.md")
        self.assertIn("**END-PARITY-6 ", text)
        self.assertIn("| END-PARITY | END-PARITY-1 to END-PARITY-7 (7) |", text)
        self.assertIn("END-PARITY-6", read("docs/reviews/acceptance-form.html"))


if __name__ == "__main__":
    unittest.main()
