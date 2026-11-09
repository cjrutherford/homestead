"""S374: Time tracking as an Everything catalog package (ADR-057)."""
import hashlib
import importlib.util
import io
import json
import re
import shutil
import subprocess
import tarfile
import tempfile
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
DEF = ROOT / 'scripts/everything/apps/time-tracking.json'
ARCHIVE = ROOT / 'third_party/everything/time-tracking-source.tar.gz'
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


class TimeTrackingPackageTest(unittest.TestCase):
    def test_definition_archive_and_pin_agree(self):
        d = json.loads(DEF.read_text())
        data = ARCHIVE.read_bytes()
        self.assertEqual(d['sha256'], hashlib.sha256(data).hexdigest())
        self.assertEqual(d['size'], len(data))
        pin = pin_document('time-tracking')
        self.assertEqual(pin['sha256'], d['sha256'])
        self.assertEqual(pin['archive'], d['archive'])
        self.assertEqual(pin['license'], 'MIT')
        self.assertEqual(pin['commit'], pin_document('tasks')['commit'])
        self.assertIn('version: ' + d['version'], (ROOT / 'catalog/time-tracking.yaml').read_text())

    def test_archive_is_plain_files_of_the_app_only_and_mit(self):
        with tarfile.open(ARCHIVE) as tar:
            members = tar.getmembers()
            names = [m.name for m in members]
            self.assertEqual(names, sorted(names))
            self.assertEqual(len(names), len(set(names)))
            self.assertEqual(len(names), 75)
            for m in members:
                self.assertTrue(m.isfile(), m.name)
                self.assertEqual((m.uid, m.gid, m.mtime), (0, 0, 0), m.name)
                self.assertTrue(m.name in {'LICENSE', 'dev/puter-localhost.mjs'} or m.name.startswith('src/time-tracking/'), m.name)
                self.assertFalse(set(Path(m.name).parts) & {'node_modules', 'dist', '..'}, m.name)
            self.assertTrue(tar.extractfile('LICENSE').read().decode().startswith('MIT License'))
            self.assertNotIn('src/time-tracking/LICENSE', names)  # the repository root licence (MIT) applies
            self.assertIn('MIT', tar.extractfile('src/time-tracking/README.md').read().decode())
            index = tar.extractfile('src/time-tracking/index.html').read()
            self.assertEqual(index.count(b'https://js.puter.com/v2/'), 1)
            self.assertNotIn(b'plausible', index)  # this app ships no analytics script
            pkg = json.loads(tar.extractfile('src/time-tracking/package.json').read())
            self.assertEqual(list(pkg['dependencies']), ['lucide'])  # the one bundled runtime dependency (ISC)

    def test_every_sdk_call_in_the_app_is_in_the_definition(self):
        d = json.loads(DEF.read_text())
        sdk = d['sdk_surface']
        allowed = set(sdk['supported']) | set(sdk['absent']) | {'ai', 'ui', 'auth', 'kv', 'fs'}
        calls_prefix_ok = ('source_app',)
        calls = set()
        with tarfile.open(ARCHIVE) as tar:
            for m in tar.getmembers():
                if not m.name.startswith('src/time-tracking/src/') or not m.name.endswith('.js') or m.name.endswith('.test.js'):
                    continue
                text = tar.extractfile(m).read().decode()
                text = re.sub(r'/\*.*?\*/', '', text, flags=re.S)
                text = re.sub(r'(?m)^\s*//.*$', '', text)
                # `puter.x`, `window.puter.x` and the calendar module's `p.ui.x`.
                for call in re.findall(r'(?:(?<![\w.\-])|(?<=window\.))puter\??\.([a-zA-Z_]+(?:\??\.[a-zA-Z]+)?)', text):
                    calls.add(call.replace('?', ''))
                for call in re.findall(r'\bp\.(ui\.[a-zA-Z]+)', text):
                    calls.add(call)
        self.assertTrue(calls, 'no puter calls found')
        for call in calls:
            self.assertTrue(call in allowed or call.split('.')[0] in ('env', 'source_app'), 'unlisted SDK call puter.' + call)
        # The app uses no ui.*, ai, net, sharing or event calls at all.
        self.assertEqual(calls, {'kv.get', 'kv.set', 'fs.read', 'fs.write', 'fs.delete',
                                 'auth.isSignedIn', 'auth.getUser', 'auth.signIn', 'auth.signOut', 'env'})
        shim = (ROOT / 'scripts/everything/puter-bridge.js').read_text()
        self.assertNotRegex(shim, r'(?m)^\s*ui\s*[:=]')  # ui.* stays absent
        # Browser APIs the app relies on that the CSP and bridge must leave alone.
        with tarfile.open(ARCHIVE) as tar:
            offline = tar.extractfile('src/time-tracking/src/lib/offline.js').read().decode()
            self.assertIn("'hourly:cache:v1'", offline)  # the app's per-person localStorage mirror
            self.assertNotIn('indexedDB', offline)

    def test_preparation_applies_patches_and_installs_the_worker(self):
        generic = load('prepare_app')
        d = generic.load_definition('time-tracking')
        self.assertEqual(d['service_worker']['mode'], 'runtime')
        self.assertEqual(len(d['patches']), 14)
        with tempfile.TemporaryDirectory() as tmp:
            base = Path(tmp)
            (base / 'bridge.js').write_text('// bridge')
            generic.prepare_app(d, ARCHIVE, base / 'bridge.js', base / 'out')
            app = base / 'out/src/time-tracking'
            index = (app / 'index.html').read_text()
            for gone in ('js.puter.com', 'plausible', 'developer.puter'):
                self.assertNotIn(gone, index)
            self.assertIn('./_homestead/puter-bridge.js', index)
            self.assertEqual((app / 'public/_homestead/puter-bridge.js').read_text(), '// bridge')
            worker = (app / 'public/sw.js').read_text()
            self.assertIn("homestead-everything-time-tracking-", worker)
            self.assertNotIn('puter.com', worker)
            self.assertIn(d['sha256'][:12], worker)
            for p in d['patches']:
                text = (base / 'out' / p['file']).read_text()
                if p['old'] not in p['new']:
                    self.assertNotIn(p['old'], text)
                if p['new']:
                    self.assertEqual(text.count(p['new']), 1)
            settings = (app / 'src/views/settings.js').read_text()
            for gone in ('developer.puter.com', 'Synced with your Puter', 'Stored in your own Puter', 'snapshot to your Puter', 'snapshot into your Puter'):
                self.assertNotIn(gone, settings)
            self.assertIn("store.user?.username || 'Homestead user'", settings)
            self.assertNotIn('developer.puter.com', (app / 'src/views/auth.js').read_text())
            self.assertEqual((app / 'src/lib/store.js').read_text().count('this.user = null;'), 1)
            self.assertIn('"build": "vite build"', (app / 'package.json').read_text())
            manifest = json.loads((app / 'public/manifest.webmanifest').read_text())
            self.assertEqual(manifest['start_url'], './')
            self.assertTrue(all(i['src'].startswith('./') for i in manifest['icons']))
            self.assertIn("'./sw.js'", (app / 'src/main.js').read_text())

    def test_dockerfile_fails_the_build_on_hosted_sdk_or_analytics_in_the_output(self):
        docker = (ROOT / 'images/everything/Dockerfile').read_text()
        self.assertIn("grep -rIlE 'js\\.puter\\.com|plausible\\.io'", docker)
        self.assertIn('exit 1', docker.split("plausible")[1].split('\n')[0])

    def test_patch_target_drift_fails_with_no_output(self):
        generic = load('prepare_app')
        d = json.loads(DEF.read_text())
        d['patches'][0]['old'] = 'this text is not in the source'
        with tempfile.TemporaryDirectory() as tmp:
            base = Path(tmp)
            (base / 'bridge.js').write_text('// b')
            with self.assertRaisesRegex(ValueError, 'UI patch target changed'):
                generic.prepare_app(d, ARCHIVE, base / 'bridge.js', base / 'out')
            self.assertFalse((base / 'out').exists())

    def test_context_copy_carries_the_new_files(self):
        for rel in ('scripts/everything/apps/time-tracking.json', 'scripts/everything/time-tracking-sw.template.js',
                    'scripts/everything/puter-bridge.js', 'internal/everything/store.go', 'internal/everything/handler.go'):
            self.assertEqual((ROOT / rel).read_bytes(), (ROOT / 'images' / 'everything' / rel).read_bytes(), rel)

    def test_catalog_is_closed_by_default_path_served_and_not_in_the_signed_manifest(self):
        y = (ROOT / 'catalog/time-tracking.yaml').read_text()
        for needle in ('serve: path', 'adapter: env-oidc', 'exclusive: true', 'HOMESTEAD_EVERYTHING_AWAIT_SIGNIN: "1"',
                       'image: homestead/everything-time-tracking:0.1.0', 'backuppaths:', 'path: data/bridge.db', 'icon: mdi:file-excel-outline'):
            self.assertIn(needle, y)
        self.assertNotIn('everything-time-tracking', (ROOT / 'catalog/craft/craft-manifest.yaml').read_text())

    def test_bookkeeping(self):
        self.assertTrue((ROOT / 'docs/slices/S374-time-tracking.md').is_file())
        self.assertRegex((ROOT / 'docs/SLICE_INDEX.md').read_text(), r'(?m)^S374 ')
        self.assertIn('S374', (ROOT / 'docs/reviews/EVERYTHING_REVIEW_PLAN.md').read_text())
        self.assertIn('## Time tracking source (S374', (ROOT / 'docs/THIRD_PARTY.md').read_text())
        deferred = (ROOT / 'docs/DEFERRED.md').read_text()
        self.assertIn('- S374:', deferred)
        for line in deferred.splitlines():
            if line.startswith('- S374:'):
                self.assertNotIn('homesteadctl', line)


if __name__ == '__main__':
    unittest.main()
