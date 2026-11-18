"""S510: supplied supporting art maps only to existing dashboard/catalog IDs."""

import re
import unittest
from pathlib import Path
import xml.etree.ElementTree as ET

REPO = Path(__file__).resolve().parent.parent


class FieldworkBrandingTest(unittest.TestCase):
    def test_navigation_glyph_references_have_local_valid_svg_assets(self):
        nav = (REPO / "dashboard/src/app/core/nav.ts").read_text(encoding="utf-8")
        glyphs = set(re.findall(r"icon: '([^']+)'", nav))
        self.assertGreaterEqual(len(glyphs), 10)
        for glyph in glyphs:
            path = REPO / "dashboard/public/icons/fieldwork" / f"{glyph}.svg"
            self.assertTrue(path.is_file(), f"missing navigation glyph: {glyph}")
            ET.parse(path)

    def test_catalog_art_matches_current_package_ids_and_keeps_fallbacks(self):
        catalog_ids = {
            re.search(r"(?m)^name:\s*([^\s#]+)", path.read_text(encoding="utf-8")).group(1)
            for path in (REPO / "catalog").glob("*.yaml")
        }
        source = (REPO / "dashboard/src/app/ui/app-icon.ts").read_text(encoding="utf-8")
        block = re.search(r"const CATALOG_ART:[^{]+\{([^}]+)\}", source, re.S)
        self.assertIsNotNone(block)
        mappings = dict(
            re.findall(r"^\s*'?([a-z0-9-]+)'?\s*:\s*'([^']+)'", block.group(1), re.M)
        )
        self.assertEqual(set(mappings), {"nextcloud", "immich", "jellyfin", "paperless-ngx", "vaultwarden"})
        self.assertTrue(set(mappings).issubset(catalog_ids))
        self.assertNotIn("home-assistant", catalog_ids)
        self.assertNotIn("collabora", catalog_ids)
        for package, artwork in mappings.items():
            path = REPO / "dashboard/public/catalog-icons" / f"{artwork}-mono.svg"
            self.assertTrue(path.is_file(), f"missing illustration for {package}")
            ET.parse(path)
        self.assertIn("APP_ICONS", source)
        self.assertIn("this.title().charAt(0).toUpperCase()", source)

    def test_existing_primary_mark_is_retained_in_dashboard_assets(self):
        original = REPO / "assets/homestead-logo.png"
        deployed = REPO / "dashboard/public/brand/homestead-logo.png"
        self.assertEqual(original.read_bytes(), deployed.read_bytes())


if __name__ == "__main__":
    unittest.main()
