"""S018 — homesteadctl Foundation conformance tests.

Guardrails for the CLI: status/version/config commands exist, every call
goes through the shared client (ADR-004 — no direct state, socket, or HTTP
code in the command), exit codes separate success/failure/usage, and the
daemon address resolves flag > env > loopback default. Runtime behavior is
covered by Go tests in cmd/homesteadctl against the real server mux.
"""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent
CTL = REPO / "cmd" / "homesteadctl"


def read_cmd(name: str) -> str:
    return (CTL / name).read_text(encoding="utf-8")


def ctl_sources() -> str:
    return "\n".join(p.read_text(encoding="utf-8") for p in CTL.glob("*.go") if not p.name.endswith("_test.go"))


class TestCliSurface(unittest.TestCase):
    def test_command_files_exist(self):
        for name in ("main.go", "run.go", "status.go", "config.go", "input.go"):
            with self.subTest(name=name):
                self.assertTrue((CTL / name).is_file())

    def test_commands_registered(self):
        src = ctl_sources()
        for cmd in ('"status"', '"version"', '"config"', '"get"', '"plan"', '"apply"'):
            with self.subTest(cmd=cmd):
                self.assertIn(cmd, src)

    def test_exit_code_contract(self):
        src = read_cmd("run.go")
        self.assertIn("exitOK", src)
        self.assertIn("exitError", src)
        self.assertIn("exitUsage", src)

    def test_server_resolution(self):
        src = read_cmd("run.go")
        self.assertIn("HOMESTEAD_SERVER", src)
        self.assertIn("http://127.0.0.1:8080", src)


class TestApiOnly(unittest.TestCase):
    def test_everything_goes_through_apiclient(self):
        self.assertIn("homestead/internal/apiclient", ctl_sources())

    def test_no_direct_state_or_transport(self):
        """The CLI never reads/writes state or speaks raw HTTP.

        desired.State appears only as a data type flowing through the
        client; storage calls stay server-side.
        """
        src = ctl_sources()
        for banned in ("net/http", "internal/store", "desired.Load(", "desired.Save(", "desired.DefaultPath("):
            with self.subTest(banned=banned):
                self.assertNotIn(banned, src)

    def test_file_input_typed_by_format(self):
        src = read_cmd("input.go")
        self.assertIn(".json", src)
        self.assertIn(".yaml", src)


if __name__ == "__main__":
    unittest.main()
