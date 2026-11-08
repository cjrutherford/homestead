"""S460 - Immich admin role comes from the immich_role claim; the welcome tour stays deferred."""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent
ADAPTERS = (REPO / "internal" / "appsso" / "adapters.go").read_text(encoding="utf-8")
MANIFEST = (REPO / "catalog" / "immich.yaml").read_text(encoding="utf-8")
DEFERRED = (REPO / "docs" / "DEFERRED.md").read_text(encoding="utf-8")


class TestImmichAdminRole(unittest.TestCase):
    def test_claim_and_role_claim(self):
        self.assertIn('const immichRoleClaim = "immich_role"', ADAPTERS)
        self.assertIn('`"admin" if admin else "user"`', ADAPTERS)
        self.assertIn('"roleClaim": immichRoleClaim', ADAPTERS)

    def test_onboarding_deferral_is_tied_to_the_pin(self):
        # Re-check the tour (Immich onboarding metadata) when the pin moves.
        self.assertIn("version: 3.3.0", MANIFEST)
        self.assertIn("S460 (Immich welcome tour", DEFERRED)

    def test_slice_recorded(self):
        index = (REPO / "docs" / "SLICE_INDEX.md").read_text(encoding="utf-8")
        self.assertIn("S460", index)
        self.assertTrue(list((REPO / "docs" / "slices").glob("S460-*.md")))


if __name__ == "__main__":
    unittest.main()
