"""S373: Inventory as an Everything catalog package (ADR-057)."""
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
DEF = ROOT / 'scripts/everything/apps/inventory.json'
ARCHIVE = ROOT / 'third_party/everything/inventory-source.tar.gz'
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


class InventoryPackageTest(unittest.TestCase):
    def test_definition_archive_and_pin_agree(self):
        d = json.loads(DEF.read_text())
        data = ARCHIVE.read_bytes()
        self.assertEqual(d['sha256'], hashlib.sha256(data).hexdigest())
        self.assertEqual(d['size'], len(data))
        pin = pin_document('inventory')
        self.assertEqual(pin['sha256'], d['sha256'])
        self.assertEqual(pin['archive'], d['archive'])
        self.assertEqual(pin['license'], 'MIT')
        self.assertEqual(pin['commit'], pin_document('tasks')['commit'])
        self.assertIn('version: ' + d['version'], (ROOT / 'catalog/inventory.yaml').read_text())

    def test_archive_is_plain_files_of_the_app_only_and_mit(self):
        with tarfile.open(ARCHIVE) as tar:
            members = tar.getmembers()
            names = [m.name for m in members]
            self.assertEqual(names, sorted(names))
            self.assertEqual(len(names), len(set(names)))
            self.assertEqual(len(names), 203)
            for m in members:
                self.assertTrue(m.isfile(), m.name)
                self.assertEqual((m.uid, m.gid, m.mtime), (0, 0, 0), m.name)
                self.assertTrue(m.name in {'LICENSE', 'dev/puter-localhost.mjs'} or m.name.startswith('src/inventory/'), m.name)
                self.assertFalse(set(Path(m.name).parts) & {'node_modules', 'dist', '..'}, m.name)
            self.assertTrue(tar.extractfile('LICENSE').read().decode().startswith('MIT License'))
            self.assertNotIn('src/inventory/LICENSE', names)  # the repository root licence (MIT) applies
            self.assertIn('MIT', tar.extractfile('src/inventory/README.md').read().decode())
            index = tar.extractfile('src/inventory/index.html').read()
            self.assertEqual(index.count(b'https://js.puter.com/v2/'), 1)
            self.assertEqual(index.count(b'plausible.io'), 1)
            pkg = json.loads(tar.extractfile('src/inventory/package.json').read())
            self.assertEqual(list(pkg['dependencies']), ['lucide'])  # the one bundled runtime dependency (ISC)

    def test_every_sdk_call_in_the_app_is_in_the_definition(self):
        d = json.loads(DEF.read_text())
        sdk = d['sdk_surface']
        allowed = set(sdk['supported']) | set(sdk['absent']) | {'ai', 'ui', 'auth', 'kv', 'fs'}
        calls_prefix_ok = ('source_app',)
        calls = set()
        with tarfile.open(ARCHIVE) as tar:
            for m in tar.getmembers():
                if not m.name.startswith('src/inventory/src/') or not m.name.endswith('.js') or m.name.endswith('.test.js'):
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
        self.assertEqual(calls, {'kv.get', 'kv.set', 'kv.list', 'kv.del', 'fs',
                                 'auth.isSignedIn', 'auth.getUser', 'auth.signIn', 'auth.signOut', 'env'})
        with tarfile.open(ARCHIVE) as tar:  # photos.js reaches puter.fs through one alias
            photos = tar.extractfile('src/inventory/src/lib/photos.js').read().decode()
        self.assertEqual(sorted(set(re.findall(r'\bdrive\.([a-z]+)\(', photos))), ['delete', 'read', 'write'])
        shim = (ROOT / 'scripts/everything/puter-bridge.js').read_text()
        self.assertNotRegex(shim, r'(?m)^\s*ui\s*[:=]')  # ui.* stays absent
        # Browser APIs the app relies on that the CSP and bridge must leave alone.
        with tarfile.open(ARCHIVE) as tar:
            offline = tar.extractfile('src/inventory/src/lib/offline.js').read().decode()
            self.assertIn("'inv-offline'", offline)
            self.assertIn('indexedDB', offline)

    def test_preparation_applies_patches_and_installs_the_worker(self):
        generic = load('prepare_app')
        d = generic.load_definition('inventory')
        self.assertEqual(d['service_worker']['mode'], 'runtime')
        self.assertEqual(len(d['patches']), 13)
        with tempfile.TemporaryDirectory() as tmp:
            base = Path(tmp)
            (base / 'bridge.js').write_text('// bridge')
            generic.prepare_app(d, ARCHIVE, base / 'bridge.js', base / 'out')
            app = base / 'out/src/inventory'
            index = (app / 'index.html').read_text()
            for gone in ('js.puter.com', 'plausible'):
                self.assertNotIn(gone, index)
            self.assertIn('./_homestead/puter-bridge.js', index)
            self.assertEqual((app / 'public/_homestead/puter-bridge.js').read_text(), '// bridge')
            worker = (app / 'public/sw.js').read_text()
            self.assertIn("homestead-everything-inventory-", worker)
            self.assertNotIn('puter.com', worker)
            self.assertIn(d['sha256'][:12], worker)
            for p in d['patches']:
                text = (base / 'out' / p['file']).read_text()
                if p['old'] not in p['new']:
                    self.assertNotIn(p['old'], text)
                if p['new']:
                    self.assertEqual(text.count(p['new']), 1)
            self.assertIn('return;', (app / 'site/plugin.js').read_text())
            self.assertIn("process.env.SITE_URL ?? ''", (app / 'site/content.js').read_text())
            self.assertNotIn('landing.html', (app / 'vite.config.js').read_text().split("'<!-- public-landing -->'")[1].split('}, publicContentPlugin')[0])
            settings = (app / 'src/views/settings.js').read_text()
            for gone in ('/docs/', '/guides/', 'developer.puter.com'):
                self.assertNotIn(gone, settings)
            self.assertEqual((app / 'src/lib/store.js').read_text().count('this.user = null;'), 1)
            self.assertIn('"build": "vite build"', (app / 'package.json').read_text())
            manifest = json.loads((app / 'public/manifest.json').read_text())
            self.assertEqual((manifest['start_url'], manifest['scope']), ('./', './'))
            self.assertTrue(all(i['src'].startswith('./') for i in manifest['icons']))

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
        for rel in ('scripts/everything/apps/inventory.json', 'scripts/everything/inventory-sw.template.js',
                    'scripts/everything/puter-bridge.js', 'internal/everything/store.go', 'internal/everything/handler.go'):
            self.assertEqual((ROOT / rel).read_bytes(), (ROOT / 'images' / 'everything' / rel).read_bytes(), rel)

    def test_catalog_is_closed_by_default_path_served_and_not_in_the_signed_manifest(self):
        y = (ROOT / 'catalog/inventory.yaml').read_text()
        for needle in ('serve: path', 'adapter: env-oidc', 'exclusive: true', 'HOMESTEAD_EVERYTHING_AWAIT_SIGNIN: "1"',
                       'image: homestead/everything-inventory:0.1.0', 'backuppaths:', 'path: data/bridge.db', 'icon: mdi:file-excel-outline'):
            self.assertIn(needle, y)
        self.assertNotIn('everything-inventory', (ROOT / 'catalog/craft/craft-manifest.yaml').read_text())

    def test_bookkeeping(self):
        self.assertTrue((ROOT / 'docs/slices/S373-inventory.md').is_file())
        self.assertRegex((ROOT / 'docs/SLICE_INDEX.md').read_text(), r'(?m)^S373 ')
        self.assertIn('S373', (ROOT / 'docs/reviews/EVERYTHING_REVIEW_PLAN.md').read_text())
        self.assertIn('## Inventory source (S373', (ROOT / 'docs/THIRD_PARTY.md').read_text())
        deferred = (ROOT / 'docs/DEFERRED.md').read_text()
        self.assertIn('- S373:', deferred)
        for line in deferred.splitlines():
            if line.startswith('- S373:'):
                self.assertNotIn('homesteadctl', line)


if __name__ == '__main__':
    unittest.main()
