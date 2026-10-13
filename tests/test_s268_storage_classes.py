import re
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent


class TestStorageClassesInSync(unittest.TestCase):
    def test_dashboard_classes_match_go_vocabulary(self):
        go = (ROOT / "internal/storage/classes.go").read_text()
        block = re.search(r"var classes = \[\]Class\{(.*?)\}", go, re.S).group(1)
        names = re.findall(r"Class(\w+)", block)
        consts = dict(re.findall(r"Class(\w+)\s+Class = \"(\w+)\"", go))
        go_classes = [consts[n] for n in names]
        ts = (ROOT / "dashboard/src/app/storage/storage.ts").read_text()
        body = re.search(r"STORAGE_CLASSES = \[(.*?)\] as const", ts, re.S).group(1)
        self.assertEqual(go_classes, re.findall(r"'(\w+)'", body))


if __name__ == "__main__":
    unittest.main()
