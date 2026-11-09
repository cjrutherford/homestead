"""Owner acceptance form: generator, data integrity, safety and freshness."""
import importlib.util
import json
import re
import shutil
import subprocess
import sys
from pathlib import Path

import unittest

ROOT = Path(__file__).resolve().parent.parent
GEN = ROOT / "scripts" / "review" / "make_acceptance_form.py"
CHECKLIST = ROOT / "docs" / "reviews" / "OWNER_ACCEPTANCE_CHECKLIST.md"
FORM = ROOT / "docs" / "reviews" / "acceptance-form.html"
SMOKE = ROOT / "scripts" / "review" / "browser-check-acceptance-form.mjs"
NODE = Path.home() / ".nvm" / "versions" / "node" / "v24.21.0" / "bin" / "node"


def load_gen():
    spec = importlib.util.spec_from_file_location("make_acceptance_form", GEN)
    mod = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(mod)
    return mod


def checklist_ids():
    return re.findall(r"^- \[[ xX]\] \*\*(\S+) — ", CHECKLIST.read_text(encoding="utf-8"), re.M)


def embedded_data(html):
    m = re.search(r'<script id="checklist-data" type="application/json">(.*?)</script>', html, re.S)
    assert m, "embedded data block missing"
    return json.loads(m.group(1))


class AcceptanceFormTest(unittest.TestCase):
    def test_checklist_ids_are_unique(self):
        ids = checklist_ids()
        assert ids
        dup = sorted({i for i in ids if ids.count(i) > 1})
        assert not dup, dup


    def test_generator_runs_and_ids_appear_exactly_once(self):
        mod = load_gen()
        html = mod.render(CHECKLIST.read_text(encoding="utf-8"))
        data = embedded_data(html)
        got = [s["id"] for sec in data["sections"] for g in sec["groups"] for s in g["steps"]]
        assert sorted(got) == sorted(checklist_ids())
        assert len(got) == len(set(got))
        for sec in data["sections"]:
            for g in sec["groups"]:
                for s in g["steps"]:
                    assert s["action"] and s["expected"], s["id"]


    def test_generator_cli_runs_isolated(self):
        r = subprocess.run([sys.executable, "-I", str(GEN), "--check"], capture_output=True, text=True)
        assert r.returncode == 0, r.stdout + r.stderr


    def test_summary_table_matches_steps(self):
        text = CHECKLIST.read_text(encoding="utf-8")
        ids = checklist_ids()
        groups = {}
        for i in ids:
            groups.setdefault(re.sub(r"-\d+[a-z]?$", "", i), []).append(i)
        table = text.split("## Results summary", 1)[1]
        rows = dict(re.findall(r"^\| (\S+) \| \S+ to \S+ \((\d+)\) \|", table, re.M))
        assert {g: int(n) for g, n in rows.items()} == {g: len(v) for g, v in groups.items()}
        assert f"| **Total** | {len(ids)} steps |" in table


    def test_no_external_resources_and_no_innerhtml(self):
        html = FORM.read_text(encoding="utf-8")
        assert not re.search(r"""<(?:script|link|img|iframe|source)\b[^>]*\b(?:src|href)\s*=\s*["']?\s*(?:https?:)?//""", html, re.I)
        assert not re.search(r"url\(\s*[\"']?(?:https?:)?//", html, re.I)
        assert "innerHTML" not in html and "outerHTML" not in html and "insertAdjacentHTML" not in html
        assert "document.write" not in html
        assert not re.search(r"\b(fetch|XMLHttpRequest|WebSocket|sendBeacon)\s*\(", html)
        assert "Do not enter passwords, tokens or secrets" in html


    def test_committed_form_is_up_to_date(self):
        mod = load_gen()
        assert FORM.read_text(encoding="utf-8") == mod.render(CHECKLIST.read_text(encoding="utf-8")), (
            "run: python3 -I scripts/review/make_acceptance_form.py"
        )


    def test_checklist_has_fresh_start_content(self):
        text = CHECKLIST.read_text(encoding="utf-8")
        assert "already claimed" not in text
        for sid in ("PLAT-DEPLOY-1", "PLAT-CLAIM-1", "PLAT-CLAIM-1a", "PLAT-SETUP-3a"):
            assert sid in checklist_ids()


    def test_headless_chrome_smoke(self):
        chrome = next((c for c in ("google-chrome", "chromium", "chromium-browser") if shutil.which(c)), None)
        node = str(NODE) if NODE.exists() else shutil.which("node")
        if not chrome or not node:
            raise unittest.SkipTest("Chrome or node not available")
        r = subprocess.run([node, str(SMOKE), str(FORM)], capture_output=True, text=True, timeout=120)
        assert r.returncode == 0, r.stdout + r.stderr
        assert "PASS" in r.stdout or "SKIP" in r.stdout


if __name__ == "__main__":
    unittest.main()
