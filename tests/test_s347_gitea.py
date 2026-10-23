"""S347 — Gitea Catalog Package conformance tests.

Guardrails for the single-container forge: a pinned Docker Hub image,
environment-only configuration with the installer locked, the routed
HTTPS root, HTTPS-only clones (no SSH), SQLite on the backed-up data
volume, and a private-by-default web surface. Behavior is covered by Go
tests in internal/catalog loading the shipped directory.
"""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent
MANIFEST = REPO / "catalog" / "gitea.yaml"


class TestGiteaContract(unittest.TestCase):
    def setUp(self):
        self.text = MANIFEST.read_text(encoding="utf-8")

    def test_package_exists(self):
        self.assertTrue(MANIFEST.is_file(), "catalog/gitea.yaml missing")
        for symbol in ("name: gitea", "version: 28.2.0",
                       "image: docker.io/gitea/gitea:28.2.0",
                       "exposes: web", "recommended: local", "allowwider: true"):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, self.text)

    def test_root_url_follows_the_route(self):
        self.assertIn('GITEA__server__ROOT_URL: "https://{{route_host}}/"',
                      self.text)

    def test_https_only_without_ssh(self):
        for symbol in ('GITEA__server__DISABLE_SSH: "true"',
                       'GITEA__server__START_SSH_SERVER: "false"',
                       'GITEA__server__PROTOCOL: http'):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, self.text)

    def test_environment_config_not_the_installer(self):
        self.assertIn('GITEA__security__INSTALL_LOCK: "true"', self.text)
        self.assertNotIn("/etc/gitea", self.text,
                         "configuration must come from the environment")

    def test_sqlite_export_on_the_data_volume(self):
        self.assertIn("kind: sqlite", self.text)
        self.assertIn("path: data/gitea/gitea.db", self.text)
        self.assertIn("target: /data", self.text)
        self.assertIn("backuppaths:", self.text)

    def test_no_declared_secrets_or_entrypoint_override(self):
        # Gitea generates its signing keys and internal token into the
        # data volume; nothing needs the S342 supervisor.
        self.assertNotIn("\nsecrets:", self.text)
        self.assertNotIn("entrypoint:", self.text)

    def test_no_latest_tag(self):
        self.assertNotIn(":latest", self.text)


if __name__ == "__main__":
    unittest.main()
