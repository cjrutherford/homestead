"""S469 - manifest `publish` field matches the ADR-061 fact table."""

import re
import unittest
from pathlib import Path

import yaml

REPO = Path(__file__).resolve().parent.parent
TABLE_ROW = re.compile(r"^\| ([a-z0-9-]+) \|")


def manifests():
    out = {}
    for path in sorted((REPO / "catalog").glob("*.yaml")):
        data = yaml.safe_load(path.read_text(encoding="utf-8"))
        out[data["name"]] = data
    return out


def adr_rows():
    rows = {}
    text = (REPO / "docs/adr/ADR-061-public-apps-at-subpath.md").read_text(encoding="utf-8")
    for line in text.splitlines():
        if TABLE_ROW.match(line):
            cells = [c.strip() for c in line.strip("|").split("|")]
            rows[cells[0]] = cells
    return rows


class TestPublishField(unittest.TestCase):
    def test_every_package_declares_publish(self):
        m = manifests()
        self.assertEqual(len(m), 46)
        for name, data in m.items():
            self.assertIn(data.get("publish"), ("path", "host", "never"), name)

    def test_rules(self):
        for name, d in manifests().items():
            wider = any(e.get("allowwider") for e in d["exposure"]["endpoints"])
            if d.get("single_origin_sign_in"):
                self.assertEqual(d["publish"], "host", name)
                self.assertEqual(d.get("publish_reason"), "single_origin", name)
            if not wider:
                self.assertEqual(d["publish"], "never", name)
            if d["publish"] == "path":
                self.assertEqual(d.get("serve"), "path", name)
            if d["publish"] == "host":
                self.assertIn(d.get("publish_reason"),
                              ("no_subpath", "own_name_adr044", "single_origin", "owner_choice"), name)
            else:
                self.assertNotIn("publish_reason", d, name)

    def test_matches_adr_fact_table(self):
        rows = adr_rows()
        m = manifests()
        self.assertEqual(set(rows), set(m))
        for name, d in m.items():
            proposed = rows[name][4]
            current = {"path": "subpath", "never": "never published", "host": "own name"}[d["publish"]]
            if d["publish"] == "host" and proposed.startswith("subpath"):
                continue  # candidate named in the table: host until its own slice
            self.assertTrue(proposed.startswith(current), f"{name}: {proposed!r} vs {current}")

    def test_go_tests_pin_it(self):
        t = (REPO / "internal/catalog/publish_test.go").read_text(encoding="utf-8")
        self.assertIn("TestPublishMatchesADR061FactTable", t)
        self.assertIn("TestPublishValidation", t)


if __name__ == "__main__":
    unittest.main()
