"""S500 - Nextcloud administrators follow the admins group."""

import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def read(rel):
    return (REPO / rel).read_text(encoding="utf-8")


class NextcloudAdmin(unittest.TestCase):
    def test_app_listener(self):
        src = read("internal/appsso/nextcloud_admin.go")
        for want in ("UserLoggedInEvent", "getBackendClassName() !== 'user_oidc'", "removeUser", "addUser"):
            self.assertIn(want, src)

    def test_enable_installs_app(self):
        self.assertIn("installAdminApp", read("internal/appsso/adapters.go"))

    def test_docs(self):
        self.assertIn("S500", read("docs/SLICE_INDEX.md"))
        self.assertIn("S500:", read("docs/DEFERRED.md"))
        self.assertIn("SRV-NEXTCLOUD-3a", read("docs/reviews/OWNER_ACCEPTANCE_CHECKLIST.md"))
        self.assertIn("hashes", read("docs/slices/S500-nextcloud-admin-from-claim.md"))


if __name__ == "__main__":
    unittest.main()
