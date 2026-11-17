"""S495 - owner guide content: builds, links resolve, commands exist."""

import importlib.util
import re
import tempfile
import unittest
from pathlib import Path

REPO = Path(__file__).resolve().parent.parent
GUIDE = REPO / "docs" / "guide"
CTL = REPO / "cmd" / "homesteadctl"

spec = importlib.util.spec_from_file_location("guide_build", REPO / "scripts" / "guide" / "build.py")
guide = importlib.util.module_from_spec(spec)
spec.loader.exec_module(guide)

PAGES = [
    "index", "getting-started", "apps", "people", "exposure",
    "storage-backups", "updates", "troubleshooting", "reference",
]


def go_sources():
    return {p.name: p.read_text() for p in CTL.glob("*.go") if not p.name.endswith("_test.go")}


def top_level_commands():
    run = (CTL / "run.go").read_text()
    return set(re.findall(r'case "([a-z-]+)"(?:, "[a-z-]+")*:', run)) | set(
        w for m in re.findall(r'case ((?:"[a-z-]+",? ?)+):', run) for w in re.findall(r'"([a-z-]+)"', m)
    )


class TestGuideContent(unittest.TestCase):
    def test_pages_exist_and_are_not_stubs(self):
        for name in PAGES:
            text = (GUIDE / f"{name}.md").read_text()
            self.assertNotIn("Stub.", text, name)
            self.assertNotIn("skeleton", text.lower(), name)
            self.assertGreater(len(text), 1200 if name != "index" else 800, name)

    def test_every_page_builds_and_links_resolve(self):
        with tempfile.TemporaryDirectory() as out:
            guide.build(GUIDE, Path(out))  # raises on a broken internal link
            built = {p.name for p in Path(out).glob("*.html")}
        for name in PAGES + ["how-this-guide-is-built"]:
            self.assertIn(f"{name}.html", built)

    def test_internal_links_point_at_real_pages(self):
        for md in GUIDE.glob("*.md"):
            for target in re.findall(r"\]\(([^)#]+\.md)(?:#[^)]*)?\)", md.read_text()):
                self.assertTrue((GUIDE / target).exists(), f"{md.name} -> {target}")

    def test_every_homesteadctl_command_exists(self):
        tops = top_level_commands()
        self.assertIn("doctor", tops)
        sources = go_sources()
        allgo = "\n".join(sources.values())
        seen = 0
        for md in GUIDE.glob("*.md"):
            text = md.read_text()
            for m in re.finditer(r"homesteadctl ((?:[a-z][a-z-]*)(?: [a-z][a-z-]*)?)", text):
                words = m.group(1).split()
                top = words[0]
                if top in ("is", "in", "action", "and", "you", "command", "with", "has", "never"):
                    continue
                if top == "alone":
                    continue
                seen += 1
                self.assertIn(top, tops, f"{md.name}: homesteadctl {m.group(1)}")
                if len(words) > 1:
                    src = sources.get(f"{top}.go", allgo)
                    sub = words[1]
                    if sub in ("and", "or", "has", "is", "to", "in", "on", "with", "now"):
                        continue
                    self.assertIn(f'"{sub}"', src + allgo if top in ("apps", "backup") else src,
                                  f"{md.name}: homesteadctl {m.group(1)}")
        self.assertGreater(seen, 40)

    def test_reference_covers_every_top_level_command(self):
        ref = (GUIDE / "reference.md").read_text()
        for cmd in top_level_commands() - {"mine"}:
            self.assertIn(f"`homesteadctl {cmd}`", ref, cmd)

    def test_required_topics_present(self):
        apps = (GUIDE / "apps.md").read_text()
        for needle in ["FigCraft", "SoundCraft", "saves by download", "LAN is open", "closed by default"]:
            self.assertIn(needle.lower(), apps.lower(), needle)
        exp = (GUIDE / "exposure.md").read_text()
        for app in ["Immich", "Gitea", "Actual Budget", "Mealie", "Calibre-Web", "BookStack", "Memos",
                    "Vikunja", "Audiobookshelf", "FreshRSS", "Vaultwarden"]:
            self.assertIn(app, exp)
        self.assertIn("FreshRSS", (GUIDE / "troubleshooting.md").read_text())
        self.assertIn("Keep the old key", (GUIDE / "storage-backups.md").read_text())

    def test_no_secrets_or_real_hosts(self):
        for md in GUIDE.glob("*.md"):
            text = md.read_text()
            self.assertNotIn("hopefulaspirations", text)
            self.assertNotIn("10.187.", text)
            self.assertNotRegex(text, r"AGE-SECRET-KEY|BEGIN [A-Z ]*PRIVATE")
            for host in re.findall(r"[a-z0-9-]+\.(?:com|net|org|io)\b", text):
                self.assertTrue(host.endswith("example.com") or host == "example.com", f"{md.name}: {host}")


if __name__ == "__main__":
    unittest.main()
