"""S083 — Route Generator conformance tests.

Guardrails for route adoption: one Caddyfile snippet per desired route
with TLS modes rendered distinctly (off pins http://, internal emits
`tls internal`, auto relies on defaults), exposure recorded as comment
for firewall planning, optional active health probing, reconciliation
that writes desired snippets and removes only stale managed files,
reload through the proxy container (Caddy validates before swapping),
and a runner-backed reconcile operation closing with a serving verify.
Behavior is covered by Go tests in internal/docker with stubbed
execution; the generator performs no serving.
"""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def read(rel: str) -> str:
    return (REPO / rel).read_text(encoding="utf-8")


class TestGeneratorContract(unittest.TestCase):
    def test_generator_shapes(self):
        src = read("internal/docker/routes.go")
        for symbol in ("func RenderRoute(", "func ReconcileRoutes(",
                       "func ReloadProxy(", "func ReconcileDefinition(",
                       '".caddy"', '"caddy.reconcile"'):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)

    def test_rendered_directives(self):
        src = read("internal/docker/routes.go")
        for symbol in ("reverse_proxy", "tls internal", "http://",
                       "health_uri", "health_interval", "Do not edit"):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)

    def test_safe_reconcile(self):
        src = read("internal/docker/routes.go")
        self.assertIn("write-routes", src)
        self.assertIn('"reload"', src)
        self.assertIn('"exec", CaddyContainerName', src)
        self.assertIn("IsDir()", src)

    def test_no_serving(self):
        src = read("internal/docker/routes.go")
        for banned in ("net/http", "HandleFunc"):
            with self.subTest(banned=banned):
                self.assertNotIn(banned, src)


if __name__ == "__main__":
    unittest.main()
