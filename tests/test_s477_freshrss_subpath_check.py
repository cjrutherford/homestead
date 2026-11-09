"""S477 - FreshRSS stays on its own name (OIDC redirect cannot take a prefix)."""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def read(rel):
    return (REPO / rel).read_text(encoding="utf-8")


class FreshRSSSubpathCheck(unittest.TestCase):
    def test_manifest_keeps_host(self):
        m = read("catalog/freshrss.yaml")
        self.assertIn("publish: host", m)
        self.assertIn("publish_reason: no_subpath", m)
        self.assertNotIn("serve: path", m)

    def test_slice_doc_and_deferred(self):
        d = read("docs/slices/S477-freshrss-subpath-check.md")
        self.assertIn("OIDCRedirectURI", d)
        self.assertIn("S477", read("docs/DEFERRED.md"))
        self.assertIn("S477", read("docs/SLICE_INDEX.md"))


if __name__ == "__main__":
    unittest.main()
