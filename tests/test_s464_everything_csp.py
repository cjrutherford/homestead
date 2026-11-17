"""S464 - narrow per-app CSP allowances for Parametric and API Client."""

import json
import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent


def read(rel):
    return (REPO / rel).read_text(encoding="utf-8")


class TestEverythingCsp(unittest.TestCase):
    def test_only_the_two_packages_opt_in(self):
        for f in sorted((REPO / "catalog").glob("*.yaml")):
            text = f.read_text(encoding="utf-8")
            self.assertEqual("HOMESTEAD_EVERYTHING_WORKER_EVAL" in text, f.name == "parametric.yaml", f.name)
            self.assertEqual("HOMESTEAD_EVERYTHING_SANDBOX_PAGE" in text, f.name == "api-client.yaml", f.name)

    def test_page_policy_never_gets_eval(self):
        go = read("internal/everything/static.go")
        page = go[go.index("func contentSecurityPolicy"):go.index("// hashedAsset")]
        self.assertNotIn("unsafe-eval'", page.replace("'wasm-unsafe-eval'", ""))
        self.assertIn('"Sec-Fetch-Dest") == "worker"', go)

    def test_sandbox_policy_has_no_network(self):
        go = read("internal/everything/static.go")
        self.assertIn("connect-src 'none'", go[go.index("const sandboxPolicy"):])

    def test_api_client_frames_the_sandbox_page(self):
        d = json.loads(read("scripts/everything/apps/api-client.json"))
        self.assertTrue(any("homestead-sandbox.html" in p["new"] for p in d["patches"]))
        targets = {a["target"] for a in d["add_files"]}
        self.assertIn("src/api-client/public/homestead-sandbox.html", targets)
        self.assertIn("src/api-client/public/homestead-sandbox.js", targets)
        self.assertNotIn("<script>", read("scripts/everything/files/api-client-sandbox.html"))

    def test_slice_recorded(self):
        self.assertIn("S464", read("docs/SLICE_INDEX.md"))
        self.assertTrue(list((REPO / "docs" / "slices").glob("S464-*.md")))


if __name__ == "__main__":
    unittest.main()
