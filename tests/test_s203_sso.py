"""S203 — Application SSO Metadata conformance tests.

Guardrails for SSO posture: manifests declare one of
unsupported/optional/recommended/required with protocols drawn
from the identity capabilities, support without a protocol and
protocols on unsupported apps both refuse, unset reads as
unsupported, and every curated package pins an honest posture —
clients optional with the protocols upstream supports, the
provider itself unsupported. Behavior is covered by Go tests in
internal/catalog.
"""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def read(rel: str) -> str:
    return (REPO / rel).read_text(encoding="utf-8")


class TestSSOMetadata(unittest.TestCase):
    def test_schema_shapes(self):
        src = read("internal/catalog/schema.go")
        for symbol in ("type SSOClass", "type SSODeclaration",
                       '"unsupported"', '"optional"',
                       '"recommended"', '"required"',
                       "func (d SSODeclaration) Validate()"):
            with self.subTest(symbol=symbol):
                self.assertIn(symbol, src)

    def test_curated_postures(self):
        catalog = REPO / "catalog"
        want = {
            "authentik": ["class: unsupported"],
            "immich": ["class: optional", "protocols: [oidc]"],
            "jellyfin": ["class: optional", "protocols: [oidc, saml]"],
            "nextcloud": ["class: optional", "protocols: [oidc, saml, ldap]"],
        }
        for name, symbols in want.items():
            text = (catalog / f"{name}.yaml").read_text(encoding="utf-8")
            for symbol in symbols:
                with self.subTest(package=name, symbol=symbol):
                    self.assertIn(symbol, text)


if __name__ == "__main__":
    unittest.main()
