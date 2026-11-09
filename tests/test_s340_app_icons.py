"""S340 — every icon a catalog manifest names is in the dashboard's icon map."""

import re
import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def camel(name: str) -> str:
    return "mdi" + "".join(p[:1].upper() + p[1:] for p in name.split("-"))


class TestAppIcons(unittest.TestCase):
    def test_manifest_icons_are_mapped(self):
        src = (REPO / "dashboard/src/app/ui/app-icons.ts").read_text(encoding="utf-8")
        manifests = sorted((REPO / "catalog").glob("*.yaml"))
        self.assertTrue(manifests)
        for m in manifests:
            found = re.search(r"^icon:\s*mdi:([a-z0-9-]+)\s*$", m.read_text(encoding="utf-8"), re.M)
            self.assertIsNotNone(found, f"{m.name} has no mdi: icon")
            name = found.group(1)
            self.assertIn(f"'{name}': {camel(name)},", src, f"{m.name}: icon {name} missing from app-icons.ts")
            self.assertIn(f"  {camel(name)},\n", src, f"{camel(name)} not imported")


if __name__ == "__main__":
    unittest.main()
