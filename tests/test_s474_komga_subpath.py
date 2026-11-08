"""S474 - Komga is served under /apps/komga/."""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def read(rel):
    return (REPO / rel).read_text(encoding="utf-8")


class KomgaSubpath(unittest.TestCase):
    def test_manifest_serves_under_the_path(self):
        m = read("catalog/komga.yaml")
        self.assertIn("publish: path", m)
        self.assertNotIn("publish_reason", m)
        self.assertIn("serve: path", m)
        self.assertIn("path: /apps/komga/", m)
        self.assertIn('SERVER_SERVLET_CONTEXT_PATH: "{{route_path}}"', m)

    def test_firstrun_uses_context_path(self):
        self.assertIn("komgaBase", read("internal/firstrun/komga.go"))

    def test_slice_doc_and_index(self):
        self.assertIn("resourceBaseUrl", read("docs/slices/S474-komga-at-subpath.md"))
        self.assertIn("S474", read("docs/SLICE_INDEX.md"))
        self.assertIn("S474", read("docs/DEFERRED.md"))


if __name__ == "__main__":
    unittest.main()
