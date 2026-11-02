"""S375: Circuits as an Everything catalog package (ADR-057)."""
import hashlib
import importlib.util
import json
import re
import tarfile
import tempfile
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
DEF = ROOT / 'scripts/everything/apps/circuits.json'
ARCHIVE = ROOT / 'third_party/everything/circuits-source.tar.gz'
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


class CircuitsPackageTest(unittest.TestCase):
    def test_definition_archive_and_pin_agree(self):
        d = json.loads(DEF.read_text())
        data = ARCHIVE.read_bytes()
        self.assertEqual(d['sha256'], hashlib.sha256(data).hexdigest())
        self.assertEqual(d['size'], len(data))
        pin = pin_document('circuits')
        self.assertEqual(pin['sha256'], d['sha256'])
        self.assertEqual(pin['archive'], d['archive'])
        self.assertEqual(pin['license'], 'MIT')
        self.assertEqual(pin['commit'], pin_document('tasks')['commit'])
        self.assertIn('version: ' + d['version'], (ROOT / 'catalog/circuits.yaml').read_text())

    def test_archive_is_plain_files_of_the_app_only_and_mit(self):
        with tarfile.open(ARCHIVE) as tar:
            members = tar.getmembers()
            names = [m.name for m in members]
            self.assertEqual(names, sorted(names))
            self.assertEqual(len(names), len(set(names)))
            self.assertEqual(len(names), 138)
            for m in members:
                self.assertTrue(m.isfile(), m.name)
                self.assertEqual((m.uid, m.gid, m.mtime), (0, 0, 0), m.name)
                self.assertTrue(m.name in {'LICENSE', 'dev/puter-localhost.mjs'} or m.name.startswith('src/circuits/'), m.name)
                self.assertFalse(set(Path(m.name).parts) & {'node_modules', 'dist', '..'}, m.name)
            self.assertTrue(tar.extractfile('LICENSE').read().decode().startswith('MIT License'))
            self.assertNotIn('src/circuits/LICENSE', names)  # the repository root licence (MIT) applies
            self.assertIn('license = "MIT"', tar.extractfile('src/circuits/wasm/kernel/Cargo.toml').read().decode())
            index = tar.extractfile('src/circuits/index.html').read()
            self.assertEqual(index.count(b'https://js.puter.com/v2/'), 1)
            self.assertNotIn(b'plausible', index)  # this app ships no analytics script
            pkg = json.loads(tar.extractfile('src/circuits/package.json').read())
            self.assertEqual(sorted(pkg['dependencies']), ['earcut', 'fflate', 'lucide', 'rbush'])
            self.assertIn('src/circuits/src/kernel/kernel.wasm', names)

    def test_every_sdk_call_in_the_app_is_in_the_definition(self):
        d = json.loads(DEF.read_text())
        sdk = d['sdk_surface']
        listed = set(sdk['supported']) | set(sdk['absent'])
        calls = set()
        with tarfile.open(ARCHIVE) as tar:
            for m in tar.getmembers():
                if not m.name.startswith('src/circuits/src/') or not m.name.endswith('.js'):
                    continue
                text = tar.extractfile(m).read().decode()
                text = re.sub(r'/\*.*?\*/', '', text, flags=re.S)
                text = re.sub(r'(?m)^\s*//.*$', '', text)
                # The app reaches the SDK as puter().x, p.x, sdk.x or sdk?.x
                # (every call goes through src/app/puter.js).
                for call in re.findall(r'(?:puter\(\)|\bsdk|\bp)\??\.((?:fs|kv|auth|ui)\??\.[a-zA-Z]+)', text):
                    calls.add(call.replace('?', ''))
        self.assertTrue({'fs.read', 'fs.write', 'fs.delete', 'fs.readdir', 'fs.rename', 'kv.get', 'kv.set'} <= calls)
        for call in calls:
            self.assertIn(call, listed, 'unlisted SDK call ' + call)
        self.assertFalse(calls & {'ai.chat', 'net.fetch'})
        shim = (ROOT / 'scripts/everything/puter-bridge.js').read_text()
        self.assertNotRegex(shim, r'(?m)^\s*ui\s*[:=]')  # ui.* stays absent
        for refused in ('share', 'getShares', 'unshare', 'listShared', 'getProfilePicture'):
            self.assertNotRegex(shim, r'(?m)^\s*(async\s+)?' + refused + r'\s*[(:]')
        self.assertRegex(shim, r'(?m)^\s*async rename\(')  # S375
        # Sharing and the Puter file picker are hidden, not left to error.
        self.assertTrue(any("id: 'file.share'" in p['old'] for p in d['patches']))
        self.assertTrue(any('Browse Puter files' in p['old'] for p in d['patches']))

    def test_the_kernel_wasm_is_shipped_so_the_policy_allows_it(self):
        go = (ROOT / 'internal/everything/static.go').read_text()
        self.assertIn("'wasm-unsafe-eval'", go)
        self.assertNotIn("'unsafe-eval'", go)

    def test_preparation_applies_patches(self):
        generic = load('prepare_app')
        d = generic.load_definition('circuits')
        self.assertIsNone(d['service_worker'])  # upstream ships and registers none
        self.assertEqual(len(d["patches"]), 19)
        with tempfile.TemporaryDirectory() as tmp:
            base = Path(tmp)
            (base / 'bridge.js').write_text('// bridge')
            generic.prepare_app(d, ARCHIVE, base / 'bridge.js', base / 'out')
            app = base / 'out/src/circuits'
            index = (app / 'index.html').read_text()
            for gone in ('js.puter.com', 'plausible', 'developer.puter'):
                self.assertNotIn(gone, index)
            self.assertIn('./_homestead/puter-bridge.js', index)
            self.assertEqual((app / 'public/_homestead/puter-bridge.js').read_text(), '// bridge')
            self.assertFalse((app / 'public/sw.js').exists())
            for p in d['patches']:
                text = (base / 'out' / p['file']).read_text()
                if p['old'] not in p['new']:
                    self.assertNotIn(p['old'], text)
                if len(p['new']) > 20:
                    self.assertEqual(text.count(p['new']), 1)
            appjs = (app / 'src/app/app.js').read_text()
            self.assertIn("{ username: 'Homestead user' }", appjs)
            self.assertNotIn('developer.puter.com', appjs)
            self.assertNotIn('developer.puter.com', (app / 'src/app/dialogs.js').read_text())
            self.assertNotIn('file.share', (app / 'src/app/menus.js').read_text())
            self.assertNotIn("id: 'file.share'", (app / 'src/app/actions.js').read_text())
            self.assertIn('"build": "vite build"', (app / 'package.json').read_text())
            manifest = json.loads((app / 'public/manifest.json').read_text())
            self.assertEqual(manifest['start_url'], './')
            self.assertTrue(all(not i['src'].startswith('/') for i in manifest['icons']))

    def test_dockerfile_fails_the_build_on_hosted_sdk_or_analytics_in_the_output(self):
        docker = (ROOT / 'images/everything/Dockerfile').read_text()
        self.assertIn("grep -rIlE 'js\\.puter\\.com|plausible\\.io'", docker)

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
        for rel in ('scripts/everything/apps/circuits.json', 'scripts/everything/puter-bridge.js',
                    'internal/everything/store.go', 'internal/everything/handler.go', 'internal/everything/static.go'):
            self.assertEqual((ROOT / rel).read_bytes(), (ROOT / 'images' / 'everything' / rel).read_bytes(), rel)

    def test_catalog_is_closed_by_default_path_served_and_not_in_the_signed_manifest(self):
        y = (ROOT / 'catalog/circuits.yaml').read_text()
        for needle in ('serve: path', 'adapter: env-oidc', 'exclusive: true', 'HOMESTEAD_EVERYTHING_AWAIT_SIGNIN: "1"',
                       'image: homestead/everything-circuits:0.1.0', 'backuppaths:', 'path: data/bridge.db', 'icon: mdi:vector-curve'):
            self.assertIn(needle, y)
        self.assertNotIn('everything-circuits', (ROOT / 'catalog/craft/craft-manifest.yaml').read_text())

    def test_bookkeeping(self):
        self.assertTrue((ROOT / 'docs/slices/S375-circuits.md').is_file())
        self.assertRegex((ROOT / 'docs/SLICE_INDEX.md').read_text(), r'(?m)^S375 ')
        self.assertIn('S375', (ROOT / 'docs/reviews/EVERYTHING_REVIEW_PLAN.md').read_text())
        self.assertIn('## Circuits source (S375', (ROOT / 'docs/THIRD_PARTY.md').read_text())
        deferred = (ROOT / 'docs/DEFERRED.md').read_text()
        self.assertIn('- S375:', deferred)
        for line in deferred.splitlines():
            if line.startswith('- S375:'):
                self.assertNotIn('homesteadctl', line)


if __name__ == '__main__':
    unittest.main()
