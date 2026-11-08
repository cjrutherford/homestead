"""S476 - Linkding is served under /apps/linkding/."""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def read(rel):
    return (REPO / rel).read_text(encoding="utf-8")


class LinkdingSubpath(unittest.TestCase):
    def test_manifest_serves_under_the_path(self):
        m = read("catalog/linkding.yaml")
        self.assertIn("publish: path", m)
        self.assertNotIn("publish_reason", m)
        self.assertIn("serve: path", m)
        self.assertIn("path: /apps/linkding/", m)
        self.assertIn("LD_CONTEXT_PATH: apps/linkding/", m)

    def test_slice_doc_and_index(self):
        self.assertIn("LD_CONTEXT_PATH", read("docs/slices/S476-linkding-at-subpath.md"))
        self.assertIn("S476", read("docs/SLICE_INDEX.md"))
        self.assertIn("S476", read("docs/DEFERRED.md"))


if __name__ == "__main__":
    unittest.main()
