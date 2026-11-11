"""S468 - a stopped Everything container shows the offline page, not a bare 502."""

import shutil
from _tools import require_tool
import subprocess
import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent
EV = REPO / "scripts" / "everything"


class TestEdgeDown(unittest.TestCase):
    def test_every_worker_treats_gateway_errors_as_offline(self):
        files = sorted(EV.glob("*-sw.template.js")) + [EV / "files" / "sudoku-sw.js"]
        self.assertEqual(8, len(files))
        for f in files:
            text = f.read_text(encoding="utf-8")
            self.assertIn("res.status === 502 || res.status === 503 || res.status === 504", text, f.name)
            self.assertIn("fetch(req).then(edgeDown).catch(", text, f.name)
            self.assertIn("You are offline", text, f.name)

    @require_tool("node")
    def test_workers_behave(self):
        r = subprocess.run(["node", str(EV / "test_edge_down_sw.cjs")], capture_output=True, text=True, timeout=60)
        self.assertEqual(0, r.returncode, r.stderr[-800:])

    def test_context_copies_match(self):
        for f in sorted(EV.glob("*-sw.template.js")):
            self.assertEqual(f.read_text(), (REPO / "images/everything/scripts/everything" / f.name).read_text(), f.name)
        self.assertEqual((EV / "files/sudoku-sw.js").read_text(), (REPO / "images/everything/scripts/everything/files/sudoku-sw.js").read_text())

    def test_slice_recorded(self):
        self.assertIn("S468", (REPO / "docs/SLICE_INDEX.md").read_text())
        self.assertTrue(list((REPO / "docs" / "slices").glob("S468-*.md")))


if __name__ == "__main__":
    unittest.main()
