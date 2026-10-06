"""S017 — Go API Client conformance tests.

Guardrails for the shared client: it covers health/version plus the full
S016 trio, propagates context, keeps validation outcomes as data, and stays
free of CLI concerns (flags, exits, printing are S018). Runtime behavior,
including the client↔server contract, is covered by Go tests in
internal/apiclient against the real server mux.
"""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def read(rel: str) -> str:
    return (REPO / rel).read_text(encoding="utf-8")


class TestClientSurface(unittest.TestCase):
    def test_operations_exist(self):
        src = read("internal/apiclient/client.go")
        for op in (
            "func (c *Client) Healthz(",
            "func (c *Client) Version(",
            "func (c *Client) GetConfig(",
            "func (c *Client) PlanConfig(",
            "func (c *Client) ApplyConfig(",
        ):
            with self.subTest(op=op):
                self.assertIn(op, src)

    def test_context_propagated(self):
        src = read("internal/apiclient/client.go")
        self.assertIn("NewRequestWithContext", src)

    def test_transport_options(self):
        src = read("internal/apiclient/client.go")
        self.assertIn("WithTimeout", src)
        self.assertIn("WithHTTPClient", src)
        self.assertIn("DefaultTimeout", src)

    def test_validation_stays_data(self):
        """Field errors return as values; only transport fails as error."""
        src = read("internal/apiclient/client.go")
        self.assertIn("StatusUnprocessableEntity", src)
        self.assertIn("StatusError", src)

    def test_no_cli_concerns(self):
        """S018 owns user interaction; the client stays a library."""
        src = read("internal/apiclient/client.go")
        for banned in ("os.Exit", '"flag"', "fmt.Print", "os.Stdout"):
            with self.subTest(banned=banned):
                self.assertNotIn(banned, src)


if __name__ == "__main__":
    unittest.main()
