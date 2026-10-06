"""S037 — Configuration Parity Enforcement conformance tests.

Parses docs/PARITY.md (the ADR-004 contract) and checks it against the
code: every daemon route has a matrix row, every row names a real route,
and every claimed CLI command and dashboard symbol exists. Adding an
endpoint, command, or UI surface without updating the matrix fails the
build; removing one fails it too.
"""

import re
import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent
SERVER = REPO / "internal" / "server"
CTL = REPO / "cmd" / "homesteadctl"
APP = REPO / "dashboard" / "src"


def parse_matrix():
    rows = []
    in_table = False
    for line in (REPO / "docs" / "PARITY.md").read_text(encoding="utf-8").splitlines():
        line = line.strip()
        if not line.startswith("|"):
            continue
        in_table = True
        cells = [cell.strip() for cell in line.strip("|").split("|")]
        if cells[0] == "Method" or set(cells[1]) == {"-"}:
            continue
        if len(cells) != 5:
            raise AssertionError(f"malformed parity row (want 5 cells): {line}")
        rows.append(
            {
                "route": f"{cells[0]} {cells[1]}",
                "cli": re.findall(r"`([^`]+)`", cells[2]) or ["n/a"],
                "dashboard": re.findall(r"`([^`]+)`", cells[3]) or ["n/a"],
            }
        )
    if not in_table or not rows:
        raise AssertionError("parity matrix table missing or empty")
    return rows


def server_routes():
    found = set()
    method_pattern = re.compile(r'HandleFunc\("([A-Z]+) ([^"]+)"')
    # The dashboard catch-all is method-less (explicit GET/HEAD patterns
    # conflict with API patterns in Go's mux); the handler serves GET and
    # HEAD and rejects the rest, so both rows stand for the one Handle.
    catchall_pattern = re.compile(r'\.Handle\("/"')
    for path in SERVER.glob("*.go"):
        if path.name.endswith("_test.go"):
            continue
        text = path.read_text(encoding="utf-8")
        found.update(f"{m} {p}" for m, p in method_pattern.findall(text))
        if catchall_pattern.search(text):
            found.update({"GET /", "HEAD /"})
    return found


def ctl_sources():
    return "\n".join(
        p.read_text(encoding="utf-8") for p in CTL.glob("*.go") if not p.name.endswith("_test.go")
    )


def dashboard_sources():
    return "\n".join(p.read_text(encoding="utf-8") for p in APP.rglob("*") if p.is_file())


class TestParityMatrix(unittest.TestCase):
    def test_every_route_has_a_row(self):
        rows = {row["route"] for row in parse_matrix()}
        for route in sorted(server_routes()):
            with self.subTest(route=route):
                self.assertIn(route, rows)

    def test_every_row_names_a_route(self):
        routes = server_routes()
        for row in parse_matrix():
            with self.subTest(route=row["route"]):
                self.assertIn(row["route"], routes)

    def test_claimed_cli_commands_exist(self):
        sources = ctl_sources()
        for row in parse_matrix():
            for token in row["cli"]:
                if token == "n/a":
                    continue
                with self.subTest(route=row["route"], token=token):
                    self.assertIn(token, sources)

    def test_claimed_dashboard_symbols_exist(self):
        sources = dashboard_sources()
        for row in parse_matrix():
            for token in row["dashboard"]:
                if token == "n/a":
                    continue
                with self.subTest(route=row["route"], token=token):
                    self.assertIn(token, sources)

    def test_matrix_covers_all_routes(self):
        self.assertGreaterEqual(len(parse_matrix()), len(server_routes()))


if __name__ == "__main__":
    unittest.main()
