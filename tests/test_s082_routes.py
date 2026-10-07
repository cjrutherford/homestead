"""S082 — Route Domain Model conformance tests.

Guardrails for edge-proxy routes: hostnames canonical (lowercase DNS,
single-label LAN names allowed, no wildcards), upstreams as host plus
port (URL pastes rejected), exposure in the shared local/overlay/public
vocabulary, provider-abstract TLS modes, optional active health probing,
and route tables with unique hosts. The model validates shape only —
policy enforcement belongs to plan/preview layers. Behavior is covered
by Go tests in internal/docker; the model performs no serving and owns
no persistence.
"""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def read(rel: str) -> str:
    return (REPO / rel).read_text(encoding="utf-8")


class TestRouteContract(unittest.TestCase):
    def test_model_shapes(self):
        src = read("internal/docker/route.go")
        for symbol in ("type Exposure", "type TLSMode", "type Hostname",
                       "type Target", "type HealthCheck", "type Route ",
                       "type RouteSet", "func (r Route) Validate()",
                       "func (s RouteSet) Validate()", "func (s RouteSet) Sorted()",
                       "func (s RouteSet) Find("):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)

    def test_shared_vocabulary(self):
        src = read("internal/docker/route.go")
        for symbol in ('"local"', '"overlay"', '"public"', '"off"',
                       '"internal"', '"auto"', "duplicate route host"):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)

    def test_shape_only(self):
        src = read("internal/docker/route.go")
        self.assertIn("lowercase", src)
        self.assertIn("not a URL", src)

    def test_no_serving_or_state(self):
        src = read("internal/docker/route.go")
        for banned in ("net/http", "HandleFunc", "sql.Open", "yaml."):
            with self.subTest(banned=banned):
                self.assertNotIn(banned, src)


if __name__ == "__main__":
    unittest.main()
