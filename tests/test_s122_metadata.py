"""S122 — Application Exposure Metadata conformance tests.

Guardrails for package declarations: recommended origins with open
or closed ceilings over a narrow-to-wide order, package-authored
warnings bounded and present, unique well-formed endpoints under
catalog naming, lookup by endpoint, and declarations only — never
application. Behavior is covered by Go tests in internal/exposure.
"""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def read(rel: str) -> str:
    return (REPO / rel).read_text(encoding="utf-8")


class TestMetadataContract(unittest.TestCase):
    def test_metadata_shapes(self):
        src = read("internal/exposure/metadata.go")
        for symbol in ("type EndpointExposure", "type PackageExposure",
                       "func (e EndpointExposure) Allows(",
                       "func (p PackageExposure) Find(",
                       "AllowWider", "Warnings"):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)


if __name__ == "__main__":
    unittest.main()
