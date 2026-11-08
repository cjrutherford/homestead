"""S488 - --route has no dashboard field while every app has one exposed service."""

import re
import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def read(rel):
    return (REPO / rel).read_text(encoding="utf-8")


class PublicRouteNotNeeded(unittest.TestCase):
    def test_every_app_exposes_one_service(self):
        for path in sorted((REPO / "catalog").glob("*.yaml")):
            exposed = re.findall(r'^\s*exposes:\s*([^\s"#]+)', path.read_text(encoding="utf-8"), re.M)
            self.assertEqual(
                len(exposed), 1,
                f"{path.name} exposes {len(exposed)} services: build the S488 route select and drop this test",
            )

    def test_docs(self):
        self.assertIn("S488 records", read("docs/SLICE_INDEX.md"))
        self.assertIn("S488:", read("docs/DEFERRED.md"))
        self.assertIn("Resolved without a UI", read("docs/SLICE_PLAN.md"))
        parity = read("docs/PARITY.md")
        self.assertIn("`--route` has no dashboard field", parity)

    def test_no_route_input_in_dashboard(self):
        for rel in ("dashboard/src/app/catalog/app-public.html",):
            p = REPO / rel
            if p.exists():
                self.assertNotIn("public-route", p.read_text(encoding="utf-8"))


if __name__ == "__main__":
    unittest.main()
