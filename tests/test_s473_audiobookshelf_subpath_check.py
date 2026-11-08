"""S473 - Audiobookshelf stays on its own name (no usable subpath)."""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def read(rel):
    return (REPO / rel).read_text(encoding="utf-8")


class AudiobookshelfSubpathCheck(unittest.TestCase):
    def test_manifest_keeps_host(self):
        m = read("catalog/audiobookshelf.yaml")
        self.assertIn("publish: host", m)
        self.assertIn("publish_reason: no_subpath", m)
        self.assertNotIn("serve: path", m)
        self.assertNotIn("ROUTER_BASE_PATH:", m)

    def test_slice_doc_and_deferred(self):
        d = read("docs/slices/S473-audiobookshelf-subpath-check.md")
        self.assertIn("_nuxt", d)
        self.assertIn("S473", read("docs/DEFERRED.md"))
        self.assertIn("S473", read("docs/SLICE_INDEX.md"))


if __name__ == "__main__":
    unittest.main()
