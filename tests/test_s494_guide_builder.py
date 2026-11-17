"""S494 - owner guide site builder and its wiring."""

import importlib.util
import re
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent
BUILD = REPO / "scripts" / "guide" / "build.py"

spec = importlib.util.spec_from_file_location("guide_build", BUILD)
guide = importlib.util.module_from_spec(spec)
spec.loader.exec_module(guide)


def render(md, page="a.md", known=("a.md", "b.md", "sub/c.md")):
    return guide.render(md, page, set(known))[0]


class TestMarkdown(unittest.TestCase):
    def test_headings_paragraphs_emphasis(self):
        out = render("# Title\n\nSome **bold** and *it* text\nsecond line.\n\n## Sub head")
        self.assertIn('<h1 id="title">Title</h1>', out)
        self.assertIn("<p>Some <strong>bold</strong> and <em>it</em> text second line.</p>", out)
        self.assertIn('<h2 id="sub-head">Sub head</h2>', out)

    def test_lists_nested_and_ordered(self):
        out = render("- one\n- two\n  - inner\n- three\n\n1. a\n2. b")
        self.assertIn("<ul><li>one</li><li>two<ul><li>inner</li></ul></li><li>three</li></ul>", out)
        self.assertIn("<ol><li>a</li><li>b</li></ol>", out)

    def test_code_block_and_span_are_escaped(self):
        out = render("```sh\necho '<b>' && *x*\n```\n\nUse `<script>` here")
        self.assertIn('<pre><code class="language-sh">echo \'&lt;b&gt;\' &amp;&amp; *x*</code></pre>', out)
        self.assertIn("<code>&lt;script&gt;</code>", out)
        self.assertNotIn("<em>", out)

    def test_table(self):
        out = render("| A | B |\n|---|---|\n| 1 | `x` |\n")
        self.assertIn("<th>A</th><th>B</th>", out)
        self.assertIn("<td>1</td><td><code>x</code></td>", out)

    def test_blockquote_and_rule(self):
        out = render("> note here\n\n---\n")
        self.assertIn("<blockquote><p>note here</p></blockquote>", out)
        self.assertIn("<hr>", out)

    def test_raw_html_is_escaped(self):
        out = render('<script>alert(1)</script>\n\n<img src=x onerror=alert(1)> **x**')
        self.assertNotIn("<script>", out)
        self.assertNotIn("<img", out)
        self.assertIn("&lt;script&gt;", out)

    def test_heading_ids_unique(self):
        out = render("## Same\n\n## Same")
        self.assertIn('id="same"', out)
        self.assertIn('id="same-2"', out)


class TestLinks(unittest.TestCase):
    def test_md_links_become_html(self):
        out = render("[B](b.md) [S](sub/c.md#frag)")
        self.assertIn('<a href="b.html">B</a>', out)
        self.assertIn('<a href="sub/c.html#frag">S</a>', out)

    def test_relative_link_from_subdir(self):
        out = render("[up](../b.md)", page="sub/c.md")
        self.assertIn('<a href="../b.html">up</a>', out)

    def test_missing_target_fails_build(self):
        with self.assertRaises(guide.BuildError):
            render("[x](nope.md)")

    def test_escaping_the_guide_fails_build(self):
        with self.assertRaises(guide.BuildError):
            render("[x](../../etc.md)")

    def test_dangerous_schemes_are_dropped(self):
        for url in ("javascript:alert(1)", "data:text/html,x", "vbscript:x", "//evil.example/x"):
            out = render("[click](%s)" % url)
            self.assertNotIn("<a ", out, url)
            self.assertIn("click", out)

    def test_external_link_has_noopener(self):
        out = render("[w](https://example.com/a?b=1&c=2)")
        self.assertIn('href="https://example.com/a?b=1&amp;c=2" rel="noopener noreferrer"', out)

    def test_images_local_only(self):
        self.assertIn('<img src="pics/a.png" alt="alt">', render("![alt](pics/a.png)"))
        out = render("![alt](https://example.com/a.png)")
        self.assertNotIn("<img", out)


class TestSite(unittest.TestCase):
    def build(self, files):
        src = Path(tempfile.mkdtemp())
        out = Path(tempfile.mkdtemp())
        for name, text in files.items():
            (src / name).parent.mkdir(parents=True, exist_ok=True)
            (src / name).write_text(text, encoding="utf-8")
        guide.build(src, out, "9.9.9")
        return out

    def test_pages_nav_pager_csp_and_version(self):
        out = self.build({
            "_nav.txt": "# c\nindex.md: Home\nb.md: Bee\n",
            "index.md": "# Home\n\n[bee](b.md)",
            "b.md": "# Bee\n",
            "hidden.md": "# Hidden\n",
        })
        page = (out / "index.html").read_text()
        self.assertIn('<a href="b.html">bee</a>', page)
        self.assertIn('<a href="b.html">Bee</a>', page)  # nav
        self.assertIn("Bee &rarr;", page)
        self.assertIn("Homestead 9.9.9", page)
        self.assertTrue((out / "hidden.html").exists())
        self.assertNotIn("hidden.html", page)
        self.assertIn('aria-current="page"', page)

    def test_csp_meta_is_strict_and_hash_matches(self):
        out = self.build({"_nav.txt": "index.md: Home\n", "index.md": "# Home\n"})
        page = (out / "index.html").read_text()
        csp = re.search(r'http-equiv="Content-Security-Policy" content="([^"]+)"', page).group(1)
        self.assertIn("default-src &#x27;none&#x27;", csp)
        self.assertNotIn("script-src", csp)
        self.assertNotIn("unsafe-inline", csp)
        self.assertIn(guide.CSS_HASH, csp)
        self.assertNotIn("<script", page)
        self.assertNotRegex(page, r'(src|href)="https?://')

    def test_nested_page_nav_links_are_relative(self):
        out = self.build({
            "_nav.txt": "index.md: Home\nsub/c.md: Cee\n",
            "index.md": "# Home\n",
            "sub/c.md": "# Cee\n",
        })
        page = (out / "sub" / "c.html").read_text()
        self.assertIn('href="../index.html"', page)

    def test_nav_must_start_with_index_and_name_real_pages(self):
        with self.assertRaises(guide.BuildError):
            self.build({"_nav.txt": "b.md: B\n", "b.md": "# B\n"})
        with self.assertRaises(guide.BuildError):
            self.build({"_nav.txt": "index.md: Home\nghost.md: G\n", "index.md": "# H\n"})

    def test_rebuild_clears_stale_pages_but_keeps_gitkeep(self):
        src, out = Path(tempfile.mkdtemp()), Path(tempfile.mkdtemp())
        (src / "_nav.txt").write_text("index.md: Home\n")
        (src / "index.md").write_text("# H\n")
        (out / "stale.html").write_text("x")
        (out / ".gitkeep").write_text("")
        guide.build(src, out)
        self.assertFalse((out / "stale.html").exists())
        self.assertTrue((out / ".gitkeep").exists())


class TestRepoGuide(unittest.TestCase):
    def test_real_guide_builds_with_isolated_python(self):
        out = tempfile.mkdtemp()
        r = subprocess.run([sys.executable, "-I", str(BUILD), "--out", out],
                           capture_output=True, text=True, timeout=60)
        self.assertEqual(r.returncode, 0, r.stderr)
        built = {p.name for p in Path(out).glob("*.html")}
        self.assertIn("index.html", built)
        self.assertIn("how-this-guide-is-built.html", built)
        src_pages = {p.stem for p in (REPO / "docs" / "guide").glob("*.md")}
        self.assertEqual({n[:-5] for n in built}, src_pages)

    def test_builder_uses_stdlib_only(self):
        text = BUILD.read_text()
        for line in re.findall(r"^(?:import|from) (\w+)", text, re.M):
            self.assertIn(line, sys.stdlib_module_names, line)


class TestWiring(unittest.TestCase):
    def read(self, rel):
        return (REPO / rel).read_text(encoding="utf-8")

    def test_makefile_release_builds_guide(self):
        mk = self.read("Makefile")
        self.assertRegex(mk, r"(?m)^guide-build:\n\tpython3 -I scripts/guide/build.py")
        self.assertRegex(mk, r"(?m)^release:.*guide-build")

    def test_installer_builds_guide_before_go_build(self):
        sh = self.read("scripts/install-systemd.sh")
        self.assertLess(sh.index("scripts/guide/build.py"), sh.index("go build"))

    def test_embed_split_and_gitignore(self):
        self.assertIn("//go:build embeddash", self.read("internal/guide/embed.go"))
        self.assertIn("//go:embed all:dist", self.read("internal/guide/embed.go"))
        self.assertIn("//go:build !embeddash", self.read("internal/guide/noembed.go"))
        gi = self.read(".gitignore")
        self.assertIn("internal/guide/dist/*", gi)
        self.assertIn("!internal/guide/dist/.gitkeep", gi)

    def test_daemon_mounts_guide_as_session_route(self):
        self.assertIn("guide.Embedded()", self.read("cmd/homestead-server/main.go"))
        b = self.read("internal/server/boundary.go")
        self.assertRegex(b, r'"GET /guide/":\s+ClassSession')
        self.assertRegex(b, r'"GET /guide/":\s+true')

    def test_dashboard_links_the_guide(self):
        self.assertIn('href="/guide/"', self.read("dashboard/src/app/home/home.html"))
        self.assertIn('href="/guide/"', self.read("dashboard/src/app/app.html"))


if __name__ == "__main__":
    unittest.main()
