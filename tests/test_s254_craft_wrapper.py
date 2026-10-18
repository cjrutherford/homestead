"""S254 — craft wrapper page conformance (static checks).

The wrapper (images/craft/homestead/) frames the unmodified craft app and
bridges its host protocol to the same-origin file path. These checks pin
the properties the slice and ADR-029/ADR-032 require; the served headers
are checked against a real image by TestRealCraftBuild.
"""

import re
import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent
WRAP = REPO / "images" / "craft" / "homestead"
JS = (WRAP / "open.js").read_text(encoding="utf-8")
HTML = (WRAP / "open.html").read_text(encoding="utf-8")
NGINX = (REPO / "images" / "craft" / "nginx.conf").read_text(encoding="utf-8")
DOCKERFILE = (REPO / "images" / "craft" / "Dockerfile").read_text(encoding="utf-8")


class TestWrapper(unittest.TestCase):
    def test_no_inline_script_or_style(self):
        self.assertNotRegex(HTML, r"<script(?![^>]*\bsrc=)")
        self.assertNotIn("style=", HTML)
        self.assertNotIn("<style", HTML)

    def test_bridge_only_hears_its_framed_app_on_its_own_origin(self):
        self.assertIn("e.source !== frame.contentWindow", JS)
        self.assertIn("e.origin !== location.origin", JS)
        # Messages are posted to the page's own origin, never "*".
        self.assertIn("location.origin", JS)
        self.assertNotRegex(JS, r"postMessage\([^)]*['\"]\*['\"]")

    def test_bridged_apps_use_the_verified_message_names(self):
        for app in ("gridcraft", "cadcraft", "pdfcraft"):
            self.assertRegex(JS, app + r": 1")
        for kind in ("ready", "dirty", "save", "failed"):
            self.assertIn("case '%s'" % kind, JS)
        self.assertIn("info.app + ':open'", JS)
        self.assertIn("?host=parent", JS)

    def test_open_only_apps_use_their_url_hooks(self):
        self.assertIn("hook.importUrl(url, name)", JS)
        self.assertIn("hook.openProject(url)", JS)
        self.assertIn("hook.addFile(url, name)", JS)
        self.assertRegex(JS, r"filmcraft: 1, effectcraft: 1")

    def test_wrapper_holds_no_credential(self):
        for word in ("Authorization", "Basic ", "apppassword", "app_password", "document.cookie", "localStorage", "sessionStorage"):
            self.assertNotIn(word, JS)
        # The launch code leaves the address before anything else runs.
        self.assertLess(JS.index("history.replaceState"), JS.index("FILES + '/session'", JS.index("history.replaceState")))
        self.assertIn("credentials: 'same-origin'", JS)

    def test_save_names_are_validated_client_side(self):
        body = JS[JS.index("function validName"):JS.index("function saveFromApp")]
        for needle in ("n !== '.'", "n !== '..'", "\\/", "\\\\", "\\u0000-\\u001f"):
            self.assertIn(needle, body)
        self.assertIn("if (!validName(name))", JS[JS.index("function saveFromApp"):])

    def test_only_same_origin_urls(self):
        self.assertNotRegex(JS + HTML, r"https?://(?!\*)")

    def test_nginx_serves_wrapper_but_never_proxies_files(self):
        self.assertIn("location ^~ /_homestead/ {", NGINX)
        self.assertIn("location ^~ /_homestead/files/ {", NGINX)
        files = NGINX[NGINX.index("location ^~ /_homestead/files/"):]
        files = files[: files.index("}")]
        self.assertIn("return 404", files)
        self.assertNotIn("proxy_pass", NGINX)
        for directive in ("default-src 'none'", "script-src 'self'", "frame-ancestors 'none'"):
            self.assertIn(directive, NGINX)

    def test_dockerfile_copies_wrapper_into_the_signed_context(self):
        self.assertIn("COPY homestead/ /usr/share/nginx/html/_homestead/", DOCKERFILE)

    def test_manifest_digest_covers_the_wrapper(self):
        import hashlib

        root = REPO / "images" / "craft"
        lines = []
        for path in sorted((p for p in root.rglob("*") if p.is_file()), key=lambda p: p.relative_to(root).as_posix().encode()):
            rel = path.relative_to(root).as_posix()
            lines.append("%s  %s\n" % (hashlib.sha256(path.read_bytes()).hexdigest(), rel))
        digest = "sha256:" + hashlib.sha256("".join(lines).encode()).hexdigest()
        manifest = (REPO / "catalog" / "craft" / "craft-manifest.yaml").read_text(encoding="utf-8")
        self.assertIn("context_digest: " + digest, manifest)
        self.assertFalse((REPO / "catalog" / "craft" / "craft-manifest.yaml.minisig").exists(), "the manifest stays unsigned")


if __name__ == "__main__":
    unittest.main()
