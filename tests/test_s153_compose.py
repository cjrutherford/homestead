"""S153 — Compose Generator conformance tests.

Guardrails for inspectable generation: images from validated
manifests with caller-positioned ports, volumes, and networks,
unknown services and duplicate or relative bindings refused before
rendering, deterministic documents with provenance headers parsing
back as Compose, stack values validating, and configuration left to
later slices. Behavior is covered by Go tests in internal/catalog.
"""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def read(rel: str) -> str:
    return (REPO / rel).read_text(encoding="utf-8")


class TestComposeContract(unittest.TestCase):
    def test_generator_shapes(self):
        src = read("internal/catalog/compose.go")
        for symbol in ("func Generate(", "func WriteStack(",
                       "type Generation", "type ServiceGeneration",
                       "type StackFiles", "unless-stopped"):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)


if __name__ == "__main__":
    unittest.main()
