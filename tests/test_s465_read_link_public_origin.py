"""S465 - read links use the approved public origin."""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def read(rel):
    return (REPO / rel).read_text(encoding="utf-8")


class TestReadLinkOrigin(unittest.TestCase):
    def test_mint_prefers_the_approved_origin(self):
        go = read("internal/everything/readroute.go")
        body = go[go.index("func (h *handler) handleReadURL"):]
        self.assertIn("h.store.publicRead", body[:1500])
        self.assertIn('r.Header.Get("Origin")', body[:1500])

    def test_image_copy_in_sync(self):
        self.assertEqual(read("internal/everything/readroute.go"),
                         read("images/everything/internal/everything/readroute.go"))

    def test_deferred_entry_removed(self):
        self.assertNotIn("Public read-link origin (medium", read("docs/DEFERRED.md"))

    def test_slice_recorded(self):
        self.assertIn("S465", read("docs/SLICE_INDEX.md"))
        self.assertTrue(list((REPO / "docs" / "slices").glob("S465-*.md")))


if __name__ == "__main__":
    unittest.main()
