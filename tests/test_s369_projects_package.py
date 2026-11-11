"""S369: Projects as an Everything catalog package (ADR-057)."""
import hashlib
import importlib.util
import io
import json
import re
import shutil
from _tools import require_tool
import subprocess
import tarfile
import tempfile
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
DEF = ROOT / 'scripts/everything/apps/projects.json'
ARCHIVE = ROOT / 'third_party/everything/projects-source.tar.gz'
PIN = (ROOT / 'third_party/everything/pin.yaml').read_text()


def load(name):
    spec = importlib.util.spec_from_file_location(name, ROOT / 'scripts/everything' / (name + '.py'))
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def pin_document(app):
    for doc in PIN.split('\n---\n'):
        meta = dict(line.split(': ', 1) for line in doc.splitlines() if line and not line.startswith('#'))
        if meta.get('app') == app:
            return meta
    raise AssertionError('no pin for ' + app)


class ProjectsPackageTest(unittest.TestCase):
    def test_definition_archive_and_pin_agree(self):
        d = json.loads(DEF.read_text())
        data = ARCHIVE.read_bytes()
        self.assertEqual(d['sha256'], hashlib.sha256(data).hexdigest())
        self.assertEqual(d['size'], len(data))
        pin = pin_document('projects')
        self.assertEqual(pin['sha256'], d['sha256'])
        self.assertEqual(pin['archive'], d['archive'])
        self.assertEqual(pin['license'], 'MIT')
        # Same upstream commit as the Tasks pin.
        self.assertEqual(pin['commit'], pin_document('tasks')['commit'])
        self.assertIn('version: ' + d['version'], (ROOT / 'catalog/projects.yaml').read_text())

    def test_archive_is_plain_files_of_the_app_only(self):
        with tarfile.open(ARCHIVE) as tar:
            members = tar.getmembers()
            names = [m.name for m in members]
            self.assertEqual(names, sorted(names))
            self.assertEqual(len(names), len(set(names)))
            for m in members:
                self.assertTrue(m.isfile(), m.name)
                self.assertEqual((m.uid, m.gid, m.mtime), (0, 0, 0), m.name)
                self.assertTrue(m.name in {'LICENSE', 'dev/puter-localhost.mjs'} or m.name.startswith('src/projects/'), m.name)
                self.assertFalse(set(Path(m.name).parts) & {'node_modules', 'dist', '..'}, m.name)
            license_text = tar.extractfile('LICENSE').read().decode()
            self.assertTrue(license_text.startswith('MIT License'))
            index = tar.extractfile('src/projects/index.html').read()
            self.assertEqual(index.count(b'https://js.puter.com/v2/'), 1)

    def test_every_sdk_call_in_the_app_is_in_the_definition(self):
        d = json.loads(DEF.read_text())
        allowed = set(d['sdk_surface']['supported']) | set(d['sdk_surface']['absent'])
        calls = set()
        with tarfile.open(ARCHIVE) as tar:
            for m in tar.getmembers():
                if not m.name.startswith('src/projects/src/') or not m.name.endswith('.js'):
                    continue
                text = tar.extractfile(m).read().decode()
                text = re.sub(r'/\*.*?\*/', '', text, flags=re.S)
                text = re.sub(r'(?m)^\s*//.*$', '', text)
                for call in re.findall(r'(?<![\w.\-])puter\??\.([a-zA-Z]+(?:\??\.[a-zA-Z]+)?)', text):
                    calls.add(call.replace('?', ''))
        self.assertTrue(calls, 'no puter calls found')
        for call in calls:
            self.assertTrue(call in allowed or call == 'env' or call.split('.')[0] == 'env', 'unlisted SDK call puter.' + call)
        # Nothing the bridge refuses is called at all.
        refused = {r for r in d['sdk_surface']['refused'] if not r.endswith('*')}
        self.assertFalse(calls & refused)
        for prefix in ('ai', 'net', 'perms', 'events', 'hosting', 'apps'):
            self.assertFalse([c for c in calls if c.split('.')[0] == prefix], prefix)
        # The two bridge-side additions Projects needs exist in the shim.
        shim = (ROOT / 'scripts/everything/puter-bridge.js').read_text()
        for name in ('async stat(', 'async readdir(', "recursive ? 'deletetree'"):
            self.assertIn(name, shim)
        self.assertNotIn('showOpenFilePicker', shim)
        self.assertNotIn('showSaveFilePicker', shim)

    def test_preparation_replaces_sdk_and_worker_and_needs_no_patch(self):
        generic = load('prepare_app')
        d = generic.load_definition('projects')
        # S467: the only patch switches the first-run welcome dialog off (ADR-059).
        self.assertEqual([p['file'] for p in d['patches']], ['src/projects/src/ui/onboarding.js'])
        with tempfile.TemporaryDirectory() as tmp:
            base = Path(tmp)
            (base / 'bridge.js').write_text('// bridge')
            generic.prepare_app(d, ARCHIVE, base / 'bridge.js', base / 'out')
            app = base / 'out/src/projects'
            index = (app / 'index.html').read_bytes()
            self.assertNotIn(b'js.puter.com', index)
            self.assertIn(b'./_homestead/puter-bridge.js', index)
            self.assertEqual((app / 'public/_homestead/puter-bridge.js').read_text(), '// bridge')
            worker = (app / 'public/sw.js').read_text()
            self.assertIn("const VERSION = '%s'" % d['sha256'][:12], worker)
            self.assertNotIn('__VERSION__', worker)
            self.assertNotIn('__ASSETS__', worker)
            self.assertNotIn('puter.com', worker)
            # Upstream's own worker cached index.html and the hosted SDK.
            self.assertNotIn('./index.html', worker.replace("relative === './index.html'", ''))

    def test_runtime_worker_mode_is_checked(self):
        generic = load('prepare_app')
        d = json.loads(DEF.read_text())
        d['service_worker']['mode'] = 'build'  # the template has no __ASSETS__
        with tempfile.TemporaryDirectory() as tmp:
            base = Path(tmp)
            (base / 'bridge.js').write_text('// b')
            with self.assertRaisesRegex(ValueError, 'invalid local service worker template'):
                generic.prepare_app(d, ARCHIVE, base / 'bridge.js', base / 'out')
            self.assertFalse((base / 'out').exists())

    @require_tool('node')
    def test_service_worker_simulation(self):
        out = subprocess.run(['node', str(ROOT / 'scripts/everything/test_projects_sw.cjs')],
                             capture_output=True, text=True, timeout=60)
        self.assertEqual(out.returncode, 0, out.stdout + out.stderr)

    def test_context_copy_carries_the_new_files(self):
        for rel in ('scripts/everything/apps/projects.json', 'scripts/everything/projects-sw.template.js',
                    'scripts/everything/prepare_app.py'):
            self.assertEqual((ROOT / rel).read_bytes(), (ROOT / 'images' / 'everything' / rel).read_bytes(), rel)

    def test_catalog_is_closed_by_default_path_served_and_not_in_the_signed_manifest(self):
        y = (ROOT / 'catalog/projects.yaml').read_text()
        for needle in ('serve: path', 'adapter: env-oidc', 'exclusive: true', 'HOMESTEAD_EVERYTHING_AWAIT_SIGNIN: "1"',
                       'image: homestead/everything-projects:0.1.0', 'backuppaths:', 'path: data/bridge.db'):
            self.assertIn(needle, y)
        self.assertNotIn('projects', (ROOT / 'catalog/craft/craft-manifest.yaml').read_text())

    def test_bookkeeping(self):
        self.assertTrue((ROOT / 'docs/slices/S369-projects.md').is_file())
        self.assertRegex((ROOT / 'docs/SLICE_INDEX.md').read_text(), r'(?m)^S369 ')
        self.assertIn('S369', (ROOT / 'docs/reviews/EVERYTHING_REVIEW_PLAN.md').read_text())
        deferred = (ROOT / 'docs/DEFERRED.md').read_text()
        self.assertIn('- S369:', deferred)
        for line in deferred.splitlines():
            if line.startswith('- S369:'):
                self.assertNotIn('homesteadctl', line)


if __name__ == '__main__':
    unittest.main()
