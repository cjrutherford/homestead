import pathlib
import re
import subprocess
import unittest

ROOT = pathlib.Path(__file__).resolve().parents[1]
APPS = "wordcraft gridcraft pdfcraft deckcraft vectorcraft photocraft lightcraft figcraft filmcraft effectcraft soundcraft designcraft cadcraft".split()


class S382(unittest.TestCase):
    def test_slice_doc_runbook_covers_every_app(self):
        doc = (ROOT / "docs/slices/S382-craft-serve-and-dns-advertisement-validation.md").read_text()
        for needle in ("craft-header-check.sh", "docker diff", "dig +short @$A", "avahi-resolve", "--approve-sign-in", "ADR-039"):
            self.assertIn(needle, doc)
        for app in APPS:
            self.assertIn(app, doc)

    def test_catalog_versions_match_signed_manifest_and_header_table(self):
        manifest = (ROOT / "catalog/craft/craft-manifest.yaml").read_text()
        table = (ROOT / "docs/architecture/CRAFT_HEADER_TABLE.md").read_text()
        for app in APPS:
            version = re.search(r"^version: (\S+)", (ROOT / f"catalog/{app}.yaml").read_text(), re.M).group(1)
            self.assertIn(f"| {app} | {version} |", table)
            self.assertIn(f"version: {version}", manifest)
        self.assertTrue((ROOT / "catalog/craft/craft-manifest.yaml.minisig").exists())

    def test_deferred_s382_lines(self):
        text = (ROOT / "docs/DEFERRED.md").read_text()
        self.assertNotIn("homesteadctl", text)
        self.assertEqual(text.count("S382 correction (stale, the manifest is signed"), 5)
        self.assertIn("S382 correction (stale, the run is recorded", text)

    def test_index_entry(self):
        idx = (ROOT / "docs/SLICE_INDEX.md").read_text()
        self.assertRegex(idx, r"(?m)^S382 re-scopes")


if __name__ == "__main__":
    unittest.main()
