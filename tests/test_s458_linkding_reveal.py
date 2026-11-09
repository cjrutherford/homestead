"""S458 - Linkding's local administrator password is revealable."""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent
MANIFEST = (REPO / "catalog" / "linkding.yaml").read_text(encoding="utf-8")


class TestLinkdingReveal(unittest.TestCase):
    def test_superuser_password_is_revealable_and_immutable(self):
        block = MANIFEST.split("- name: superuser_password", 1)[1].split("- name: oidc_client_secret", 1)[0]
        self.assertIn("immutable: true", block)
        self.assertIn("reveal: true", block)
        self.assertIn("LD_SUPERUSER_PASSWORD_FILE", block)

    def test_slice_recorded(self):
        index = (REPO / "docs" / "SLICE_INDEX.md").read_text(encoding="utf-8")
        self.assertIn("S458", index)
        self.assertTrue(list((REPO / "docs" / "slices").glob("S458-*.md")))


if __name__ == "__main__":
    unittest.main()
