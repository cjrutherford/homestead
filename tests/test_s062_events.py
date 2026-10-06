"""S062 — Operation Event Stream conformance tests.

Guardrails for progress streaming: poll-backed SSE state events that
close at terminal, single-record fetch, CLI get/follow with outcome
exits, and a dashboard EventSource helper without progress UI (S064
owns that). WebSockets stay out (new dep, no need). Behavior is covered
by Go tests against the real server mux plus Vitest specs.
"""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent
CTL = REPO / "cmd" / "homesteadctl"


def read(rel: str) -> str:
    return (REPO / rel).read_text(encoding="utf-8")


def read_dash(rel: str) -> str:
    return (REPO / "dashboard" / rel).read_text(encoding="utf-8")


def ctl_sources() -> str:
    return "\n".join(
        p.read_text(encoding="utf-8") for p in CTL.glob("*.go") if not p.name.endswith("_test.go")
    )


class TestStreamContract(unittest.TestCase):
    def test_endpoints(self):
        src = read("internal/server/operations.go")
        self.assertIn("GET /api/v1/operations/{id}", src)
        self.assertIn("GET /api/v1/operations/{id}/events", src)
        self.assertIn("text/event-stream", src)
        self.assertIn("event: state", src)

    def test_poll_backed_and_bounded(self):
        src = read("internal/server/operations.go")
        self.assertIn("EventPollInterval", src)
        self.assertIn("Terminal(", src)
        self.assertIn("Flusher", src)

    def test_no_websocket(self):
        for rel in ("internal/server/operations.go", "internal/apiclient/operations.go",
                    "go.mod"):
            src = read(rel)
            for banned in ("gorilla", "websocket", "Websocket"):
                with self.subTest(file=rel, banned=banned):
                    self.assertNotIn(banned, src)

    def test_client_and_cli(self):
        self.assertIn("func (c *Client) GetOperation(", read("internal/apiclient/operations.go"))
        self.assertIn("func (c *Client) StreamOperation(", read("internal/apiclient/operations.go"))
        src = ctl_sources()
        for cmd in ("runOperationsGet", "runOperationsFollow", '"operations"'):
            with self.subTest(cmd=cmd):
                self.assertIn(cmd, src)

    def test_dashboard_helper_without_progress_ui(self):
        src = read_dash("src/app/core/api.service.ts")
        self.assertIn("operation(", src)
        self.assertIn("operationEvents(", src)
        self.assertIn("EventSource", src)
        names = [p.name for p in (REPO / "dashboard" / "src" / "app").rglob("*")]
        self.assertNotIn("progress", " ".join(names))

    def test_json_tags(self):
        src = read("internal/operation/operation.go")
        self.assertIn('`json:"status"`', src)
        self.assertIn('`json:"steps"`', src)


if __name__ == "__main__":
    unittest.main()
