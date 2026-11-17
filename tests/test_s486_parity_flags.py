"""S486 - parity test tightening and the flag table.

Owner rule (2026-11-08): every homesteadctl action that goes through the
API has a real dashboard control. Only `claim token`, `login
--print-token` and the root-socket `admins/recover` stay CLI-only.

* every PARITY.md dashboard symbol has a non-spec call site (and, for an
  API wrapper, a caller outside api.service.ts);
* a row with a CLI token and a dashboard `n/a` is only `admins/recover`;
* docs/PARITY_FLAGS.md lists every homesteadctl flag (parsed from
  cmd/homesteadctl) with its dashboard field or an allowed n/a reason, and
  each named dashboard field exists in non-spec code.
"""

import re
import sys
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent))
import test_s037_parity as parity  # noqa: E402

REPO = parity.REPO
CTL = parity.CTL
APP = parity.APP
CLI_ONLY_ROWS = {"POST /api/v1/admins/recover"}
CLI_ONLY_FLAGS = {("admin.go", "--print-token"), ("claim.go", "--file"), ("recover.go", "--socket")}
REASONS = {"output", "plan-digest", "actor", "route", "session", "retired", "cli-only"}


def non_spec_files():
    return [p for p in APP.rglob("*") if p.is_file() and ".spec." not in p.name]


def flag_names(path):
    text = path.read_text(encoding="utf-8")
    names = set()
    for n in re.findall(r'\bset\.(?:String|Bool|Int|Duration|Var)\("([A-Za-z0-9-]+)"', text):
        names.add(n)
    for n in re.findall(r'\bset\.(?:StringVar|BoolVar|IntVar)\(&?[\w.\[\]]+,\s*"([A-Za-z0-9-]+)"', text):
        names.add(n)
    out = {("-" if len(n) == 1 else "--") + n for n in names}
    out |= set(re.findall(r'"(--[a-z][a-z0-9-]*)"', text))
    return out


def ctl_flags():
    found = set()
    for path in sorted(CTL.glob("*.go")):
        if path.name.endswith("_test.go"):
            continue
        found.update((path.name, f) for f in flag_names(path))
    return found


def parse_flag_table():
    rows = []
    for line in (REPO / "docs" / "PARITY_FLAGS.md").read_text(encoding="utf-8").splitlines():
        line = line.strip()
        if not line.startswith("|"):
            continue
        cells = [c.strip() for c in line.strip("|").split("|")]
        if cells[0] == "Source" or set(cells[1]) <= {"-"}:
            continue
        if len(cells) != 5:
            raise AssertionError(f"malformed flag row (want 5 cells): {line}")
        src = cells[0].strip("`")
        flag = cells[1].strip("`")
        dash = cells[4]
        if dash.startswith("n/a"):
            m = re.search(r"`([a-z-]+)`", dash)
            rows.append((src, flag, None, m.group(1) if m else ""))
        else:
            rows.append((src, flag, dash.strip("`"), None))
    return rows


class TestTightenedParity(unittest.TestCase):
    def test_dashboard_symbols_have_a_non_spec_call_site(self):
        files = {p: p.read_text(encoding="utf-8") for p in non_spec_files()}
        for row in parity.parse_matrix():
            for token in row["dashboard"]:
                if token == "n/a":
                    continue
                pat = re.compile(r"(?<![\w-])" + re.escape(token) + r"(?![\w-])")
                hits = [p for p, s in files.items() if pat.search(s)]
                with self.subTest(route=row["route"], token=token):
                    self.assertTrue(hits, "only specs (or nothing) mention it")
                    if token == "n/a":
                        continue
                    outside = [p for p in hits if p.name != "api.service.ts"]
                    self.assertTrue(outside, "no caller outside api.service.ts")

    def test_cli_rows_have_a_dashboard_control(self):
        for row in parity.parse_matrix():
            if row["cli"] != ["n/a"] and row["dashboard"] == ["n/a"]:
                with self.subTest(route=row["route"]):
                    self.assertIn(row["route"], CLI_ONLY_ROWS)

    def test_cli_only_rows_are_exactly_the_exception(self):
        found = {
            r["route"]
            for r in parity.parse_matrix()
            if r["cli"] != ["n/a"] and r["dashboard"] == ["n/a"]
        }
        self.assertEqual(found, CLI_ONLY_ROWS)


class TestFlagTable(unittest.TestCase):
    def test_every_flag_is_in_the_table(self):
        table = {(s, f) for s, f, _, _ in parse_flag_table()}
        for src, flag in sorted(ctl_flags()):
            with self.subTest(source=src, flag=flag):
                self.assertIn((src, flag), table)

    def test_table_names_no_unknown_flag(self):
        real = ctl_flags()
        for src, flag, _, _ in parse_flag_table():
            with self.subTest(source=src, flag=flag):
                self.assertIn((src, flag), real)

    def test_dashboard_fields_exist_in_non_spec_code(self):
        text = "\n".join(p.read_text(encoding="utf-8") for p in non_spec_files())
        for src, flag, field, reason in parse_flag_table():
            if field is None:
                continue
            with self.subTest(source=src, flag=flag, field=field):
                self.assertRegex(text, r"(?<![\w])" + re.escape(field) + r"(?![\w])")

    def test_na_reasons_are_allowed(self):
        for src, flag, field, reason in parse_flag_table():
            if field is not None:
                continue
            with self.subTest(source=src, flag=flag):
                self.assertIn(reason, REASONS)
                if reason == "actor":
                    self.assertEqual(flag, "--actor")
                if reason == "route":
                    self.assertEqual(flag, "--route")
                if reason == "cli-only":
                    self.assertIn((src, flag), CLI_ONLY_FLAGS)
                if (src, flag) in CLI_ONLY_FLAGS:
                    self.assertEqual(reason, "cli-only")


if __name__ == "__main__":
    unittest.main()
