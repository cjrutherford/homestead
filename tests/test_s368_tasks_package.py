"""S368: Tasks as an ordinary catalog package built from the generic Everything recipe (ADR-057)."""
import hashlib
import importlib.util
import json
import re
import tempfile
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
DEF = ROOT / 'scripts/everything/apps/tasks.json'
ARCHIVE = ROOT / 'third_party/everything/tasks-source.tar.gz'


def load(name):
    spec = importlib.util.spec_from_file_location(name, ROOT / 'scripts/everything' / (name + '.py'))
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


class TasksPackageTest(unittest.TestCase):
    def test_definition_pins_the_shipped_archive(self):
        d = json.loads(DEF.read_text())
        data = ARCHIVE.read_bytes()
        self.assertEqual(d['sha256'], hashlib.sha256(data).hexdigest())
        self.assertEqual(d['size'], len(data))
        self.assertIn('sha256: ' + d['sha256'], (ROOT / 'third_party/everything/pin.yaml').read_text())
        self.assertRegex(d['version'], r'^\d+\.\d+\.\d+$')
        self.assertIn('version: ' + d['version'], (ROOT / 'catalog/tasks.yaml').read_text())

    def test_sdk_surface_fails_closed_for_ai_net_and_sharing(self):
        d = json.loads(DEF.read_text())
        self.assertEqual(set(d['sdk_surface']['disabled']), {'ai', 'net', 'sharing'})
        self.assertIn('fs.getReadURL', d['sdk_surface']['refused'])

    def test_generic_prepare_matches_the_s367_wrapper(self):
        generic, wrapper = load('prepare_app'), load('prepare_tasks')
        with tempfile.TemporaryDirectory() as tmp:
            base = Path(tmp)
            (base / 'bridge.js').write_text('// b')
            generic.prepare_app(generic.load_definition('tasks'), ARCHIVE, base / 'bridge.js', base / 'a')
            wrapper.prepare(ARCHIVE, base / 'bridge.js', base / 'b')
            a = sorted(p.relative_to(base / 'a').as_posix() for p in (base / 'a').rglob('*') if p.is_file())
            b = sorted(p.relative_to(base / 'b').as_posix() for p in (base / 'b').rglob('*') if p.is_file())
            self.assertEqual(a, b)

    def test_generic_prepare_refuses_a_changed_patch_target(self):
        generic = load('prepare_app')
        d = json.loads(DEF.read_text())
        d['patches'][0]['old'] = 'this text does not occur'
        with tempfile.TemporaryDirectory() as tmp:
            base = Path(tmp)
            (base / 'bridge.js').write_text('// b')
            with self.assertRaisesRegex(ValueError, 'UI patch target changed'):
                generic.prepare_app(d, ARCHIVE, base / 'bridge.js', base / 'out')
            self.assertFalse((base / 'out').exists())

    def test_generic_prepare_refuses_an_unknown_app(self):
        with self.assertRaises(Exception):
            load('prepare_app').load_definition('../tasks')

    def test_recipe_is_outside_the_signed_craft_context(self):
        self.assertTrue((ROOT / 'images/everything/Dockerfile').is_file())
        self.assertFalse((ROOT / 'images/craft/Dockerfile.everything').exists())
        self.assertNotIn('tasks', (ROOT / 'catalog/craft/craft-manifest.yaml').read_text())
        docker = (ROOT / 'images/everything/Dockerfile').read_text()
        for line in re.findall(r'^ARG (?:PYTHON|NODE|GOLANG|RUNTIME)=(\S+)', docker, re.M):
            self.assertIn('@sha256:', line)
        self.assertIn('npm ci --ignore-scripts', docker)

    def test_installer_ships_the_context_and_sources_separately(self):
        script = (ROOT / 'scripts/install-systemd.sh').read_text()
        self.assertIn('$CATALOG_DST/everything/context', script)
        self.assertIn('$CATALOG_DST/everything/sources', script)

    def test_logout_posts_with_csrf(self):
        js = (ROOT / 'scripts/everything/puter-bridge.js').read_text()
        body = js[js.index('async signOut()'):]
        body = body[:body.index('\n      },')]
        self.assertIn("method: 'POST'", body)
        self.assertIn('X-Homestead-CSRF', body)
        self.assertNotIn('location.assign(config.signOutUrl)', body)

    def test_bookkeeping(self):
        self.assertTrue((ROOT / 'docs/slices/S368-tasks.md').is_file())
        self.assertTrue((ROOT / 'docs/adr/ADR-057-everything-apps-as-catalog-packages.md').is_file())
        self.assertIn('ADR-057', (ROOT / 'docs/adr/README.md').read_text())
        self.assertRegex((ROOT / 'docs/SLICE_INDEX.md').read_text(), r'(?m)^S368 ')
        self.assertTrue((ROOT / 'docs/reviews/EVERYTHING_REVIEW_PLAN.md').is_file())
        deferred = (ROOT / 'docs/DEFERRED.md').read_text()
        self.assertIn('- S368:', deferred)
        for line in deferred.splitlines():
            if line.startswith('- S368:'):
                self.assertNotIn('homesteadctl', line)


if __name__ == '__main__':
    unittest.main()
