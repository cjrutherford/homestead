#!/usr/bin/env python3
"""Build the Homestead owner guide (S494): docs/guide/*.md -> static HTML.

Standard library only; run as `python3 -I scripts/guide/build.py`.

Supported Markdown subset: headings, paragraphs, bullet and numbered lists
(one nesting level by indentation), fenced code, blockquotes, horizontal
rules, tables, links, images, **bold**, *italic* and `code`. Raw HTML in the
source is escaped, never passed through. Relative links to .md files become
.html and must point at a real page (the build fails otherwise). Each page
carries a strict Content-Security-Policy meta tag; the page CSS is inline and
allowed by hash, so no script, font, frame or network request is possible and
the site works from a plain directory.

Navigation comes from `_nav.txt` in the source directory: one
`file.md: Title` per line, `#` comments allowed. The first entry is the home
page. Pages not listed are still built, but get no sidebar entry.
"""
import argparse
import base64
import hashlib
import html
import re
import shutil
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parent.parent.parent

CSS = """
:root{color-scheme:light dark;--bg:#fbfaf7;--fg:#1d2420;--muted:#5b6660;--line:#d9ddd6;--accent:#2f6b4f;--code:#eef0ea}
@media(prefers-color-scheme:dark){:root{--bg:#161a17;--fg:#e6e9e4;--muted:#9aa59e;--line:#2c332e;--accent:#7fc3a0;--code:#1f2521}}
*{box-sizing:border-box}
body{margin:0;background:var(--bg);color:var(--fg);font:16px/1.6 system-ui,sans-serif;display:flex;min-height:100vh}
nav{width:17rem;flex:none;border-right:1px solid var(--line);padding:1.25rem 1rem;position:sticky;top:0;align-self:flex-start;max-height:100vh;overflow:auto}
nav .brand{font-weight:700;margin:0 0 1rem}
nav ul{list-style:none;margin:0;padding:0}
nav li a{display:block;padding:.3rem .5rem;border-radius:.3rem;color:var(--fg);text-decoration:none}
nav li a:hover{background:var(--code)}
nav li a[aria-current=page]{background:var(--code);font-weight:600;color:var(--accent)}
main{flex:1;max-width:48rem;padding:1.5rem 2rem 4rem;min-width:0}
a{color:var(--accent)}
h1,h2,h3,h4{line-height:1.25;margin:1.8rem 0 .6rem}h1{margin-top:0}
code,pre{background:var(--code);border-radius:.3rem;font:14px/1.5 ui-monospace,monospace}
code{padding:.1rem .3rem}pre{padding:.8rem 1rem;overflow:auto}pre code{padding:0;background:none}
table{border-collapse:collapse;display:block;overflow:auto}
th,td{border:1px solid var(--line);padding:.35rem .7rem;text-align:left}
blockquote{margin:1rem 0;padding:.1rem 1rem;border-left:4px solid var(--accent);color:var(--muted)}
img{max-width:100%}hr{border:0;border-top:1px solid var(--line)}
.pager{display:flex;justify-content:space-between;gap:1rem;margin-top:3rem;border-top:1px solid var(--line);padding-top:1rem}
footer{color:var(--muted);font-size:.85rem;margin-top:2rem}
@media(max-width:46rem){body{display:block}nav{width:auto;position:static;max-height:none;border-right:0;border-bottom:1px solid var(--line)}main{padding:1rem}}
""".strip()

CSS_HASH = "sha256-" + base64.b64encode(hashlib.sha256(CSS.encode()).digest()).decode()
CSP = (
    "default-src 'none'; img-src 'self' data:; style-src '%s'; "
    "base-uri 'none'; form-action 'none'" % CSS_HASH
)

SAFE_URL = re.compile(r"^(https?://|mailto:|#|[^:]*$)", re.I)


class BuildError(Exception):
    pass


def slug(text):
    s = re.sub(r"[^a-z0-9]+", "-", re.sub(r"<[^>]+>", "", text).lower()).strip("-")
    return s or "section"


def link_target(url, page, known):
    """Return the href to emit for url, or None when it is not allowed."""
    raw = html.unescape(url).strip()
    if not SAFE_URL.match(raw) or raw.startswith("//") or "\\" in raw:
        return None
    if re.match(r"^(https?:|mailto:|#)", raw, re.I):
        return raw
    path, _, frag = raw.partition("#")
    if path.endswith(".md"):
        target = _normalize((Path(page).parent / path).as_posix())
        if target not in known:
            raise BuildError("%s: link to missing page %s" % (page, raw))
        raw = path[:-3] + ".html" + ("#" + frag if frag else "")
    return raw


def _normalize(p):
    parts = []
    for seg in p.split("/"):
        if seg == "..":
            if not parts:
                raise BuildError("link escapes the guide: %s" % p)
            parts.pop()
        elif seg not in ("", "."):
            parts.append(seg)
    return "/".join(parts)


def inline(text, page, known):
    """Render inline Markdown. text is raw source; all of it is escaped."""
    codes = []

    def stash(m):
        codes.append("<code>%s</code>" % html.escape(m.group(1), quote=False))
        return "\x00%d\x00" % (len(codes) - 1)

    text = re.sub(r"`([^`]+)`", stash, text)
    text = html.escape(text, quote=False)

    def image(m):
        href = link_target(m.group(2), page, known)
        alt = html.escape(html.unescape(m.group(1)))
        if href is None or re.match(r"^(https?:|mailto:)", href, re.I):
            return alt  # remote images would break the offline, CSP-bound page
        return '<img src="%s" alt="%s">' % (html.escape(href), alt)

    def link(m):
        href = link_target(m.group(2), page, known)
        if href is None:
            return m.group(1)
        ext = ' rel="noopener noreferrer"' if re.match(r"^https?:", href, re.I) else ""
        return '<a href="%s"%s>%s</a>' % (html.escape(href), ext, m.group(1))

    text = re.sub(r"!\[([^\]]*)\]\(([^)\s]+)\)", image, text)
    text = re.sub(r"\[([^\]]+)\]\(([^)\s]+)\)", link, text)
    text = re.sub(r"\*\*([^*]+)\*\*", r"<strong>\1</strong>", text)
    text = re.sub(r"(?<![\w*])\*([^*\s][^*]*)\*(?![\w*])", r"<em>\1</em>", text)
    return re.sub(r"\x00(\d+)\x00", lambda m: codes[int(m.group(1))], text)


LIST_RE = re.compile(r"^(\s*)([-*+]|\d+\.)\s+(.*)$")
TABLE_SEP = re.compile(r"^\s*\|?\s*:?-{3,}:?\s*(\|\s*:?-{3,}:?\s*)*\|?\s*$")


def split_row(line):
    line = line.strip().strip("|")
    return [c.strip() for c in line.split("|")]


def render(md, page, known):
    """Return (html_body, title, headings)."""
    lines = md.replace("\r\n", "\n").split("\n")
    out, headings, title = [], [], None
    used = {}
    i = 0

    def para_end(j):
        return (
            j >= len(lines) or not lines[j].strip() or lines[j].startswith(("```", "#", ">"))
            or LIST_RE.match(lines[j]) or re.match(r"^(-{3,}|\*{3,})\s*$", lines[j])
            or (lines[j].lstrip().startswith("|") and j + 1 < len(lines) and TABLE_SEP.match(lines[j + 1]))
        )

    while i < len(lines):
        line = lines[i]
        if not line.strip():
            i += 1
        elif line.startswith("```"):
            lang = re.sub(r"[^\w+-]", "", line[3:].strip())
            i += 1
            buf = []
            while i < len(lines) and not lines[i].startswith("```"):
                buf.append(lines[i])
                i += 1
            i += 1
            cls = ' class="language-%s"' % lang if lang else ""
            out.append("<pre><code%s>%s</code></pre>" % (cls, html.escape("\n".join(buf), quote=False)))
        elif re.match(r"^#{1,6}\s", line):
            level = len(line) - len(line.lstrip("#"))
            text = line[level:].strip().rstrip("#").strip()
            sid = slug(text)
            used[sid] = used.get(sid, 0) + 1
            if used[sid] > 1:
                sid += "-%d" % used[sid]
            if level == 1 and title is None:
                title = text
            if level in (2, 3):
                headings.append((level, sid, text))
            out.append('<h%d id="%s">%s</h%d>' % (level, sid, inline(text, page, known), level))
            i += 1
        elif re.match(r"^(-{3,}|\*{3,})\s*$", line):
            out.append("<hr>")
            i += 1
        elif line.startswith(">"):
            buf = []
            while i < len(lines) and lines[i].startswith(">"):
                buf.append(lines[i][1:].lstrip())
                i += 1
            out.append("<blockquote><p>%s</p></blockquote>" % inline(" ".join(buf), page, known))
        elif line.lstrip().startswith("|") and i + 1 < len(lines) and TABLE_SEP.match(lines[i + 1]):
            head = split_row(line)
            i += 2
            rows = []
            while i < len(lines) and lines[i].lstrip().startswith("|"):
                rows.append(split_row(lines[i]))
                i += 1
            t = "<table><thead><tr>%s</tr></thead><tbody>" % "".join(
                "<th>%s</th>" % inline(c, page, known) for c in head)
            for r in rows:
                t += "<tr>%s</tr>" % "".join("<td>%s</td>" % inline(c, page, known) for c in r)
            out.append(t + "</tbody></table>")
        elif LIST_RE.match(line):
            html_list, i = render_list(lines, i, page, known)
            out.append(html_list)
        else:
            buf = [line.strip()]
            i += 1
            while not para_end(i):
                buf.append(lines[i].strip())
                i += 1
            out.append("<p>%s</p>" % inline(" ".join(buf), page, known))
    return "\n".join(out), title, headings


def render_list(lines, i, page, known):
    m = LIST_RE.match(lines[i])
    base = len(m.group(1).expandtabs(4))
    tag = "ol" if m.group(2)[0].isdigit() else "ul"
    items = []
    while i < len(lines):
        m = LIST_RE.match(lines[i])
        if not m or len(m.group(1).expandtabs(4)) != base:
            break
        text = [m.group(3)]
        i += 1
        sub = ""
        while i < len(lines) and lines[i].strip():
            n = LIST_RE.match(lines[i])
            if n and len(n.group(1).expandtabs(4)) > base:
                sub, i = render_list(lines, i, page, known)
            elif n:
                break
            else:
                text.append(lines[i].strip())
                i += 1
        items.append("<li>%s%s</li>" % (inline(" ".join(text), page, known), sub))
    return "<%s>%s</%s>" % (tag, "".join(items), tag), i


def read_nav(src):
    nav = []
    nav_file = src / "_nav.txt"
    if not nav_file.is_file():
        raise BuildError("missing %s" % nav_file)
    for raw in nav_file.read_text(encoding="utf-8").splitlines():
        raw = raw.strip()
        if not raw or raw.startswith("#"):
            continue
        name, sep, title = raw.partition(":")
        if not sep or not name.strip().endswith(".md") or not title.strip():
            raise BuildError("bad _nav.txt line: %r (want 'file.md: Title')" % raw)
        nav.append((name.strip(), title.strip()))
    if not nav:
        raise BuildError("_nav.txt lists no pages")
    return nav


def page_html(rel, title, body, headings, nav, version):
    depth = rel.count("/")
    up = "../" * depth
    idx = [n for n, _ in nav].index(rel) if rel in [n for n, _ in nav] else None
    items = []
    for name, label in nav:
        cur = ' aria-current="page"' if name == rel else ""
        items.append('<li><a href="%s%s"%s>%s</a></li>' % (up, name[:-3] + ".html", cur, html.escape(label)))
    pager = ""
    if idx is not None:
        prev = '<a href="%s%s">&larr; %s</a>' % (up, nav[idx - 1][0][:-3] + ".html", html.escape(nav[idx - 1][1])) if idx > 0 else "<span></span>"
        nxt = '<a href="%s%s">%s &rarr;</a>' % (up, nav[idx + 1][0][:-3] + ".html", html.escape(nav[idx + 1][1])) if idx + 1 < len(nav) else "<span></span>"
        pager = '<div class="pager">%s%s</div>' % (prev, nxt)
    return (
        '<!doctype html>\n<html lang="en"><head><meta charset="utf-8">\n'
        '<meta http-equiv="Content-Security-Policy" content="%s">\n'
        '<meta name="viewport" content="width=device-width,initial-scale=1">\n'
        '<meta name="referrer" content="no-referrer">\n'
        "<title>%s - Homestead guide</title>\n<style>%s</style></head><body>\n"
        '<nav aria-label="Guide"><p class="brand">Homestead guide</p><ul>%s</ul></nav>\n'
        "<main>\n%s\n%s\n<footer>Homestead %s owner guide. Built into this appliance; nothing here leaves it.</footer>\n"
        "</main></body></html>\n"
    ) % (html.escape(CSP), html.escape(title), CSS, "".join(items), body, pager, html.escape(version))


def build(src, out, version="dev"):
    src, out = Path(src), Path(out)
    nav = read_nav(src)
    pages = sorted(p.relative_to(src).as_posix() for p in src.rglob("*.md"))
    known = set(pages)
    for name, _ in nav:
        if name not in known:
            raise BuildError("_nav.txt lists missing page %s" % name)
    if out.exists():
        for child in out.iterdir():
            if child.name == ".gitkeep":
                continue
            shutil.rmtree(child) if child.is_dir() else child.unlink()
    out.mkdir(parents=True, exist_ok=True)
    for rel in pages:
        body, title, headings = render((src / rel).read_text(encoding="utf-8"), rel, known)
        title = title or dict(nav).get(rel, rel)
        dest = out / (rel[:-3] + ".html")
        dest.parent.mkdir(parents=True, exist_ok=True)
        dest.write_text(page_html(rel, title, body, headings, nav, version), encoding="utf-8")
    # Home page: the first nav entry answers at index.html.
    home = nav[0][0]
    if home != "index.md":
        raise BuildError("the first _nav.txt entry must be index.md")
    for ext in ("png", "jpg", "jpeg", "gif", "svg", "webp"):
        for img in src.rglob("*." + ext):
            dest = out / img.relative_to(src)
            dest.parent.mkdir(parents=True, exist_ok=True)
            shutil.copyfile(img, dest)
    return pages


def main(argv=None):
    ap = argparse.ArgumentParser(description=__doc__.split("\n")[0])
    ap.add_argument("--src", default=str(ROOT / "docs" / "guide"))
    ap.add_argument("--out", default=str(ROOT / "internal" / "guide" / "dist"))
    ap.add_argument("--version-file", default=str(ROOT / "VERSION"))
    args = ap.parse_args(argv)
    vf = Path(args.version_file)
    version = vf.read_text(encoding="utf-8").strip() if vf.is_file() else "dev"
    try:
        pages = build(args.src, args.out, version)
    except BuildError as e:
        print("guide build failed: %s" % e, file=sys.stderr)
        return 1
    print("guide: built %d pages into %s" % (len(pages), args.out))
    return 0


if __name__ == "__main__":
    sys.exit(main())
