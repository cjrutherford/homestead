"""S471 - move plan from a subdomain to a subpath, read `publish`, docs."""

import re
import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def read(rel):
    return (REPO / rel).read_text(encoding="utf-8")


class MoveToSubpath(unittest.TestCase):
    def test_never_publish_field_is_gone(self):
        self.assertNotIn("never_publish:", read("internal/catalog/schema.go").replace("yaml:\"never_publish\"", ""))
        for p in sorted((REPO / "catalog").glob("*.yaml")):
            self.assertNotIn("\nnever_publish:", "\n" + p.read_text(encoding="utf-8"), p.name)
        self.assertIn("m.PublishForm() != PublishNever", read("internal/catalog/schema.go"))
        self.assertIn("PublishForm() != catalog.PublishNever", read("internal/server/doctorlive.go"))

    def test_plan_says_links_stop_and_nothing_redirects(self):
        code = read("internal/server/appmove.go")
        self.assertIn("Old bookmarks and links on %s stop working", code)
        self.assertIn("nothing redirects from it", code)
        # Removal is last: the removal steps are appended after sign-in.
        self.assertLess(code.index("m.signInStep(plan"), code.index("movePublicRemoveSteps(m.st"))
        for step in ("verify-path", "connector", "delete-dns", "restore-dns"):
            self.assertIn('Name: "%s"' % step, code)

    def test_cookie_warning_and_fact(self):
        self.assertIn('yaml:"cookie_path_root"', read("internal/catalog/schema.go"))
        self.assertIn("cookiePathWarning(f.manifest)", read("internal/server/apppublic.go"))
        self.assertIn("cookiePathWarning(mf)", read("internal/server/appmove.go"))

    def test_doctor_check(self):
        code = read("internal/server/doctorlive.go")
        self.assertIn('"subpath-publish"', code)
        self.assertIn("move available", code)

    def test_surfaces_and_docs(self):
        self.assertIn("public_from", read("dashboard/src/app/core/api.service.ts"))
        self.assertIn("data-move-public", read("dashboard/src/app/catalog/app-move.ts"))
        self.assertIn("public_from", read("internal/apiclient/apppublic.go"))
        self.assertIn("stops working (no redirect)", read("cmd/homesteadctl/apppublic.go"))
        self.assertIn("S471", read("docs/PARITY.md"))
        self.assertIn("S471", read("docs/SLICE_INDEX.md"))
        self.assertIn("PLAT-TUNNEL-8", read("docs/reviews/OWNER_ACCEPTANCE_CHECKLIST.md"))
        self.assertTrue(re.search(r"^- S471:", read("docs/DEFERRED.md"), re.M))
        self.assertTrue((REPO / "docs/slices/S471-move-plan-subdomain-to-subpath.md").exists())

    def test_no_app_moved(self):
        # S472 moved Navidrome, S474 Komga, S475 Paperless-ngx, S476 Linkding; S473 to S477 move the rest; this slice changes no package serving.
        for name in ("audiobookshelf", "freshrss"):
            self.assertNotRegex(read("catalog/%s.yaml" % name), r"(?m)^serve: path")


if __name__ == "__main__":
    unittest.main()
