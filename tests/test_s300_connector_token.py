"""S300 — Tunnel connector token stays out of argv.

cloudflared reads the run token from TUNNEL_TOKEN, which the unit loads
from the root-only token.env. The command line is world-readable in
/proc, so the unit must never pass the token as an argument, and the
install script restarts a running connector so a changed unit applies.
"""

import configparser
import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent
UNIT = REPO / "systemd" / "homestead-tunnel.service"


def load_unit() -> configparser.ConfigParser:
    parser = configparser.ConfigParser(strict=True, interpolation=None)
    parser.read(UNIT)
    return parser


class TestConnectorToken(unittest.TestCase):
    def test_token_comes_from_environment_file(self):
        service = load_unit()["Service"]
        self.assertEqual(service["EnvironmentFile"], "/etc/homestead/tunnel/token.env")

    def test_exec_start_has_no_token(self):
        exec_start = load_unit()["Service"]["ExecStart"]
        self.assertNotIn("--token", exec_start)
        self.assertNotIn("TUNNEL_TOKEN", exec_start)
        self.assertTrue(exec_start.endswith(" run"), exec_start)

    def test_connector_writes_token_env(self):
        src = (REPO / "internal/server/tunnelconnector.go").read_text(encoding="utf-8")
        self.assertIn('"TUNNEL_TOKEN="+runToken', src)
        self.assertIn('"token.env"', src)

    def test_install_restarts_running_connector(self):
        script = (REPO / "scripts/install-systemd.sh").read_text(encoding="utf-8")
        reload_at = script.index("systemctl daemon-reload")
        restart_at = script.index("systemctl try-restart homestead-tunnel.service")
        self.assertLess(reload_at, restart_at)


if __name__ == "__main__":
    unittest.main()
