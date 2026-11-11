"""S501 - signing commands in docs and scripts use minisign -l."""

import re
import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent
SIGN = re.compile(r"minisign\b.*\s-[A-Za-z]*S")
LEGACY = re.compile(r"\s-[A-Za-z]*l\b")


class ReleaseSigningDocs(unittest.TestCase):
    def test_signing_commands_use_legacy_flag(self):
        files = list((REPO / "docs").rglob("*.md")) + list((REPO / "scripts").glob("*.sh"))
        self.assertTrue(files)
        bad = []
        for f in files:
            for n, line in enumerate(f.read_text(encoding="utf-8").splitlines(), 1):
                if SIGN.search(line) and not LEGACY.search(line):
                    bad.append(f"{f.relative_to(REPO)}:{n}: {line.strip()}")
        self.assertEqual(bad, [])


if __name__ == "__main__":
    unittest.main()
