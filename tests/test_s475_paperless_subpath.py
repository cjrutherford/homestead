"""S475 - Paperless-ngx is served under /apps/paperless-ngx/."""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def read(rel):
    return (REPO / rel).read_text(encoding="utf-8")


class PaperlessSubpath(unittest.TestCase):
    def test_manifest_serves_under_the_path(self):
        m = read("catalog/paperless-ngx.yaml")
        self.assertIn("publish: path", m)
        self.assertNotIn("publish_reason", m)
        self.assertIn("serve: path", m)
        self.assertIn("path: /apps/paperless-ngx/", m)
        self.assertIn('PAPERLESS_FORCE_SCRIPT_NAME: "{{route_path}}"', m)
        self.assertIn('PAPERLESS_STATIC_URL: "{{route_path}}/static/"', m)

    def test_slice_doc_and_index(self):
        self.assertIn("base href", read("docs/slices/S475-paperless-at-subpath.md"))
        self.assertIn("S475", read("docs/SLICE_INDEX.md"))
        self.assertIn("S475", read("docs/DEFERRED.md"))


if __name__ == "__main__":
    unittest.main()
