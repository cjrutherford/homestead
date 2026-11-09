"""S491 - craft saves recorded as upstream gaps (docs only)."""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent
APPS = {"figcraft": "FigCraft", "soundcraft": "SoundCraft", "designcraft": "DesignCraft"}


def read(rel):
    return (REPO / rel).read_text(encoding="utf-8")


class CraftSavesUpstream(unittest.TestCase):
    def test_request_drafts(self):
        for app, name in APPS.items():
            text = read(f"docs/upstream/{app}-save-export.md")
            self.assertIn(name, text)
            self.assertIn("Acceptance criteria", text)
            self.assertIn("IndexedDB", text)
            self.assertIn("not filed", text)

    def test_deferred_entry_per_app(self):
        deferred = read("docs/DEFERRED.md")
        for app in APPS:
            self.assertIn(f"docs/upstream/{app}-save-export.md", deferred)

    def test_docs(self):
        self.assertIn("S491 records", read("docs/SLICE_INDEX.md"))
        self.assertIn("replaced by S491 upstream gap", read("docs/SLICE_PLAN.md"))
        self.assertTrue((REPO / "docs/slices/S491-craft-saves-upstream.md").exists())

    def test_checklist_notes_and_form(self):
        md = read("docs/reviews/OWNER_ACCEPTANCE_CHECKLIST.md")
        for sid in ("CRAFT-FIGCRAFT-4", "CRAFT-FIGCRAFT-5", "CRAFT-SOUNDCRAFT-5"):
            block = md.split(f"**{sid} ")[1].split("- [ ] **")[0]
            self.assertIn("Known upstream gap (S491)", block, sid)
        self.assertEqual(md.count("Known upstream gap (S491)"), 3)
        block = md.split("**CRAFT-DESIGNCRAFT-5 ")[1].split("- [ ] **")[0]
        self.assertIn("S491a", block)
        self.assertIn("Known upstream gap (S491)", read("docs/reviews/acceptance-form.html"))


if __name__ == "__main__":
    unittest.main()
