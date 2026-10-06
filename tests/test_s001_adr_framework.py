"""S001 — ADR Framework conformance tests.

Verifies that every record in docs/adr/ follows the framework defined in
docs/adr/README.md: naming, required sections in order, valid status
lifecycle, continuous numbering, and README index consistency.

Stdlib only, consistent with the S000 test harness.
"""

import re
import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent
ADR_DIR = REPO / "docs" / "adr"

FILENAME_RE = re.compile(r"^(ADR-\d{3})-([a-z0-9]+(?:-[a-z0-9]+)*)\.md$")
VALID_STATUS_RE = re.compile(r"^(Proposed|Accepted|Superseded by ADR-\d{3})$")
REQUIRED_SECTIONS = ["## Status", "## Decision", "## Consequences"]


def adr_files():
    return sorted(
        p for p in ADR_DIR.glob("*.md") if FILENAME_RE.match(p.name)
    )


class TestAdrNaming(unittest.TestCase):
    def test_all_files_follow_naming_convention(self):
        md_files = sorted(p for p in ADR_DIR.glob("*.md") if p.name != "README.md")
        self.assertGreater(len(md_files), 0, "adr directory must contain records")
        for path in md_files:
            with self.subTest(file=path.name):
                self.assertIsNotNone(
                    FILENAME_RE.match(path.name),
                    f"{path.name} must match ADR-<NNN>-<kebab-case-slug>.md",
                )

    def test_numbering_continuous_from_001(self):
        ids = [FILENAME_RE.match(p.name).group(1) for p in adr_files()]
        numbers = [int(i.split("-")[1]) for i in ids]
        self.assertEqual(numbers, list(range(1, len(numbers) + 1)))

    def test_no_duplicate_numbers(self):
        ids = [FILENAME_RE.match(p.name).group(1) for p in adr_files()]
        self.assertEqual(len(ids), len(set(ids)))


class TestAdrFormat(unittest.TestCase):
    def test_required_sections_present_in_order(self):
        for path in adr_files():
            with self.subTest(file=path.name):
                content = path.read_text(encoding="utf-8")
                positions = [content.find(s) for s in REQUIRED_SECTIONS]
                for section, pos in zip(REQUIRED_SECTIONS, positions):
                    self.assertNotEqual(
                        pos, -1, f"{path.name} is missing {section}"
                    )
                self.assertEqual(
                    positions, sorted(positions), f"{path.name} sections out of order"
                )

    def test_title_heading_present(self):
        for path in adr_files():
            with self.subTest(file=path.name):
                first_line = path.read_text(encoding="utf-8").splitlines()[0]
                self.assertTrue(
                    first_line.startswith("# "),
                    f"{path.name} must open with a '# <title>' heading",
                )

    def test_status_value_valid(self):
        for path in adr_files():
            with self.subTest(file=path.name):
                content = path.read_text(encoding="utf-8")
                status_block = content.split("## Status", 1)[1].split("## ", 1)[0]
                lines = [ln.strip() for ln in status_block.strip().splitlines() if ln.strip()]
                self.assertGreater(len(lines), 0, f"{path.name} has empty Status")
                self.assertTrue(
                    VALID_STATUS_RE.match(lines[0]),
                    f"{path.name} has invalid status: {lines[0]!r}",
                )

    def test_superseded_records_name_successor(self):
        for path in adr_files():
            content = path.read_text(encoding="utf-8")
            status_block = content.split("## Status", 1)[1].split("## ", 1)[0]
            first = next(
                (ln.strip() for ln in status_block.strip().splitlines() if ln.strip()),
                "",
            )
            if first.startswith("Superseded"):
                with self.subTest(file=path.name):
                    successor = first.split("ADR-")[1]
                    matches = list(ADR_DIR.glob(f"ADR-{successor}-*.md"))
                    self.assertEqual(
                        len(matches), 1, f"{path.name} successor ADR-{successor} missing"
                    )


class TestAdrIndex(unittest.TestCase):
    def test_readme_exists_and_states_rules(self):
        readme = (ADR_DIR / "README.md").read_text(encoding="utf-8")
        self.assertIn("## Status lifecycle", readme)
        self.assertIn("Superseded by ADR-", readme)
        self.assertIn("docs/DEFERRED.md", readme)

    def test_readme_index_lists_every_record(self):
        readme = (ADR_DIR / "README.md").read_text(encoding="utf-8")
        for path in adr_files():
            adr_id = FILENAME_RE.match(path.name).group(1)
            with self.subTest(adr=adr_id):
                self.assertIn(adr_id, readme)

    def test_readme_index_matches_status(self):
        readme = (ADR_DIR / "README.md").read_text(encoding="utf-8")
        for path in adr_files():
            adr_id = FILENAME_RE.match(path.name).group(1)
            content = path.read_text(encoding="utf-8")
            status_block = content.split("## Status", 1)[1].split("## ", 1)[0]
            first = next(
                (ln.strip() for ln in status_block.strip().splitlines() if ln.strip()),
                "",
            )
            with self.subTest(adr=adr_id):
                if first == "Accepted":
                    self.assertRegex(readme, rf"\| {adr_id} \|.*Accepted")
                elif first == "Proposed":
                    self.assertRegex(readme, rf"\| {adr_id} \|.*Proposed")


if __name__ == "__main__":
    unittest.main()
