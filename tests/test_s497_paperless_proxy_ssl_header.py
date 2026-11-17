"""S497 - Paperless-ngx trusts the edge's forwarded scheme."""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def read(rel):
    return (REPO / rel).read_text(encoding="utf-8")


class PaperlessProxySSL(unittest.TestCase):
    def test_manifest(self):
        self.assertIn('PAPERLESS_PROXY_SSL_HEADER: \'["HTTP_X_FORWARDED_PROTO", "https"]\'', read("catalog/paperless-ngx.yaml"))

    def test_docs(self):
        self.assertIn("CSRF", read("docs/slices/S497-paperless-public-csrf.md"))
        self.assertIn("S497 fixes", read("docs/SLICE_INDEX.md"))
        self.assertIn("S497:", read("docs/DEFERRED.md"))
        self.assertIn("CSRF verification failed", read("docs/reviews/OWNER_ACCEPTANCE_CHECKLIST.md"))


if __name__ == "__main__":
    unittest.main()
