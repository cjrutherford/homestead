"""S467 - no first-run dialogs or Puter wording in Everything apps (ADR-059)."""

import importlib.util
import json
import tempfile
import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent
EV = REPO / "scripts" / "everything"
APPS = sorted(p.stem for p in (EV / "apps").glob("*.json"))
ONBOARDING = ["tasks", "time-tracking", "projects", "expense", "inventory"]
PHRASES = [
    "Puter cloud", "Puter account", "Puter storage", "Puter Drive", "Tasks on Puter",
    "Waiting for Puter", "Puter username", "Puter user", "Powered by Puter",
]


def load_prepare():
    spec = importlib.util.spec_from_file_location("prepare_app", EV / "prepare_app.py")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def read(rel):
    return (REPO / rel).read_text(encoding="utf-8")


class TestNoFirstRun(unittest.TestCase):
    def test_wording_rules_are_phrases_not_identifiers(self):
        rules = json.loads((EV / "wording.json").read_text())["rules"]
        self.assertTrue(rules)
        for old, new in rules:
            self.assertNotIn("puter", new.lower(), old)
            self.assertNotRegex(old, r"^[A-Za-z_]+$", "a bare identifier would break code: " + old)

    def test_welcome_dialogs_are_patched_off(self):
        for app in ONBOARDING:
            d = json.loads(read(f"scripts/everything/apps/{app}.json"))
            hits = [p for p in d["patches"] if p["file"].endswith("ui/onboarding.js") and "return false;" in p["new"]]
            self.assertEqual(1, len(hits), app)

    def test_three_d_has_no_splash_or_quick_setup(self):
        d = json.loads(read("scripts/everything/apps/three-d.json"))
        news = {p["file"]: p["new"] for p in d["patches"]}
        self.assertIn("return false;", news["src/3d/src/ui/quicksetup.ts"])
        self.assertIn("show_splash: false", news["src/3d/src/app/prefs.ts"])
        startup = [p for p in d["patches"] if p["file"].endswith("ui/topbar.ts") and "showSplash({ startup: true })" in p["old"]]
        self.assertEqual(1, len(startup))
        self.assertNotIn("showSplash", startup[0]["new"])

    def test_prepared_sources_have_no_puter_phrases(self):
        prep = load_prepare()
        bridge = EV / "puter-bridge.js"
        for app in APPS:
            d = prep.load_definition(app)
            archive = REPO / "third_party" / "everything" / d["archive"]
            if not archive.exists():
                self.skipTest("pinned archives not present")
            with tempfile.TemporaryDirectory() as tmp:
                out = Path(tmp) / "out"
                prep.prepare_app(d, archive, bridge, out)
                for f in out.rglob("*"):
                    rel = "/" + f.relative_to(out).as_posix()
                    if not f.is_file() or f.suffix not in (".js", ".ts", ".html") or any(s in rel for s in prep.WORDING["skip"]):
                        continue
                    if "/_homestead/" in rel:
                        continue
                    text = "\n".join(
                        line for line in f.read_text(encoding="utf-8", errors="ignore").splitlines()
                        if not line.lstrip().startswith(("//", "*", "/*"))
                    )
                    for phrase in PHRASES:
                        self.assertNotIn(phrase, text, f"{app} {rel}")
                if app in ONBOARDING:
                    sub = d["subtree"]
                    self.assertIn("return false; // Homestead (S467", (out / sub / "src/ui/onboarding.js").read_text())

    def test_build_context_carries_the_rules(self):
        self.assertIn("wording.json", read("scripts/everything/sync-context.sh"))
        self.assertEqual(read("scripts/everything/wording.json"), read("images/everything/scripts/everything/wording.json"))
        self.assertIn("wording.json", read("scripts/everything/make_source_offer.py"))

    def test_slice_recorded(self):
        self.assertIn("S467", read("docs/SLICE_INDEX.md"))
        self.assertTrue(list((REPO / "docs" / "slices").glob("S467-*.md")))


if __name__ == "__main__":
    unittest.main()
