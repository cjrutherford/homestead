"""S377: 3D as an Everything catalog package (ADR-057)."""
import hashlib
import importlib.util
import json
import re
import tarfile
import tempfile
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
DEF = ROOT / 'scripts/everything/apps/three-d.json'
ARCHIVE = ROOT / 'third_party/everything/three-d-source.tar.gz'
PIN = (ROOT / 'third_party/everything/pin.yaml').read_text()


def load(name):
    spec = importlib.util.spec_from_file_location(name, ROOT / 'scripts/everything' / (name + '.py'))
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def pin_document(app):
    for doc in PIN.split('\n---\n'):
        meta = dict(line.split(': ', 1) for line in doc.splitlines() if line and not line.startswith('#') and ': ' in line)
        if meta.get('app') == app:
            return meta
    raise AssertionError('no pin for ' + app)


class ThreeDPackageTest(unittest.TestCase):
    def test_definition_archive_and_pin_agree(self):
        d = json.loads(DEF.read_text())
        data = ARCHIVE.read_bytes()
        self.assertEqual(d['sha256'], hashlib.sha256(data).hexdigest())
        self.assertEqual(d['size'], len(data))
        pin = pin_document('three-d')
        self.assertEqual(pin['sha256'], d['sha256'])
        self.assertEqual(pin['archive'], d['archive'])
        self.assertEqual(pin['license'], 'AGPL-3.0-only')
        self.assertEqual(pin['commit'], pin_document('tasks')['commit'])
        self.assertIn('version: ' + d['version'], (ROOT / 'catalog/three-d.yaml').read_text())

    def test_archive_is_plain_files_of_the_app_only_and_agpl(self):
        with tarfile.open(ARCHIVE) as tar:
            members = tar.getmembers()
            names = [m.name for m in members]
            self.assertEqual(names, sorted(names))
            self.assertEqual(len(names), len(set(names)))
            self.assertEqual(len(names), 1898)
            for m in members:
                self.assertTrue(m.isfile(), m.name)
                self.assertEqual((m.uid, m.gid, m.mtime), (0, 0, 0), m.name)
                self.assertTrue(m.name in {'LICENSE', 'dev/puter-localhost.mjs'} or m.name.startswith('src/3d/'), m.name)
                self.assertFalse(set(Path(m.name).parts) & {'node_modules', 'dist', '..'}, m.name)
            self.assertTrue(tar.extractfile('LICENSE').read().decode().startswith('MIT License'))  # the repository's
            self.assertIn('GNU AFFERO', tar.extractfile('src/3d/LICENSE').read().decode()[:80])  # the app's own
            index = tar.extractfile('src/3d/index.html').read().decode()
            self.assertNotRegex(index, r'js\.puter\.com|plausible|puter\.com')  # upstream never loads the SDK
            self.assertFalse([n for n in names if n.endswith('.wasm')])  # the physics module is inline base64 in a script
            pkg = json.loads(tar.extractfile('src/3d/package.json').read())
            self.assertEqual(sorted(pkg['dependencies']), ['@dimforge/rapier3d-compat', '@fontsource/inter', '@fontsource/jetbrains-mono',
                                                           'fzstd', 'opentype.js', 'three', 'three-bvh-csg', 'three-gpu-pathtracer', 'three-mesh-bvh', 'xatlas-web'])

    def test_sdk_surface_is_what_the_entry_point_and_the_app_use(self):
        d = json.loads(DEF.read_text())
        sdk = d['sdk_surface']
        listed = set(sdk['supported']) | set(sdk['absent'])
        boot = (ROOT / 'scripts/everything/files/3d-person-storage.ts').read_text()
        calls = {re.sub(r'\s', '', c) for c in re.findall(r'\b(?:bridge|puter)[!?]*\.((?:fs|kv|auth|ui)\s*\.\s*[a-zA-Z]+)', boot)}
        self.assertEqual(calls, {'auth.getUser', 'kv.get', 'kv.set'})
        for call in calls:
            self.assertIn(call, listed)
        self.assertEqual(set(sdk['disabled']), {'ai', 'net', 'sharing', 'python'})
        shim = (ROOT / 'scripts/everything/puter-bridge.js').read_text()
        self.assertNotRegex(shim, r'(?m)^\s*ui\s*[:=]')
        self.assertRegex(shim, r'(?m)^\s*async rename\(')  # S376, reused

    def test_the_inline_wasm_rule_is_reused_and_unsafe_eval_stays_refused(self):
        go = (ROOT / 'internal/everything/static.go').read_text()
        self.assertEqual(go.count("scripts += \" 'wasm-unsafe-eval'\""), 1)  # one rule, not a second copy
        self.assertNotIn("'unsafe-eval'", go.replace("'wasm-unsafe-eval'", ''))
        self.assertIn('WebAssembly.instantiate', go)

    def test_preparation_applies_patches_and_new_generic_features(self):
        generic = load('prepare_app')
        d = generic.load_definition('three-d')
        self.assertIsNone(d['service_worker'])
        self.assertIsNone(d['sdk_tag'])
        self.assertEqual(len(d['patches']), 17)
        with tempfile.TemporaryDirectory() as tmp:
            base = Path(tmp)
            (base / 'bridge.js').write_text('// bridge')
            generic.prepare_app(d, ARCHIVE, base / 'bridge.js', base / 'out')
            app = base / 'out/src/3d'
            index = (app / 'index.html').read_text()
            self.assertLess(index.index('./_homestead/puter-bridge.js'), index.index('/src/homestead/boot.ts'))
            self.assertNotIn('/src/main.ts', index)
            self.assertEqual((app / 'public/_homestead/puter-bridge.js').read_text(), '// bridge')
            self.assertEqual((app / 'src/homestead/boot.ts').read_text(), (ROOT / 'scripts/everything/files/3d-person-storage.ts').read_text())
            self.assertIn("base: './'", (app / 'vite.config.ts').read_text())
            for p in d['patches']:
                text = (base / 'out' / p['file']).read_text()
                if p['old'] not in p['new']:
                    self.assertNotIn(p['old'], text)
                if len(p['new']) > 20:
                    self.assertGreaterEqual(text.count(p['new']), 1)
            src = app / 'src'
            self.assertNotIn('restoreAssistant();', (src / 'main.ts').read_text())
            topbar = (src / 'ui/topbar.ts').read_text()
            self.assertNotIn('AI Assistant', topbar)
            self.assertNotIn('Report a Bug', topbar)
            self.assertIn("SDK_URL = ''", (src / 'assistant/puter.ts').read_text())
            py = (src / 'scripting/python.ts').read_text()
            self.assertNotIn('jsdelivr', py)
            self.assertNotIn('jsdelivr', (app / 'THIRD_PARTY_NOTICES.md').read_text())
            runner = (src / 'scripting/runner.ts').read_text()
            self.assertNotIn("new Function('__scope'", runner)  # would stop the app at start under the policy
            self.assertIn('cannot run on Homestead', runner)
            boot = (src / 'homestead/boot.ts').read_text()
            for needle in ('browser-settings/local-storage.v1', 'IDBFactory.prototype.open', 'getDirectory', 'QuotaExceededError', 'bridgeReady'):
                self.assertIn(needle, boot)

    def test_add_files_refuses_to_overwrite_an_upstream_file(self):
        generic = load('prepare_app')
        d = json.loads(DEF.read_text())
        d['add_files'] = [{'target': 'src/3d/index.html', 'source': '3d-person-storage.ts'}]
        with tempfile.TemporaryDirectory() as tmp:
            base = Path(tmp)
            (base / 'bridge.js').write_text('// b')
            with self.assertRaises(ValueError):
                generic.prepare_app(d, ARCHIVE, base / 'bridge.js', base / 'out')
            self.assertFalse((base / 'out').exists())
        d['add_files'] = [{'target': '../escape.ts', 'source': '3d-person-storage.ts'}]
        with tempfile.TemporaryDirectory() as tmp:
            base = Path(tmp)
            (base / 'bridge.js').write_text('// b')
            with self.assertRaises(ValueError):
                generic.prepare_app(d, ARCHIVE, base / 'bridge.js', base / 'out')

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
        for rel in ('scripts/everything/apps/three-d.json', 'scripts/everything/files/3d-person-storage.ts', 'scripts/everything/prepare_app.py',
                    'scripts/everything/puter-bridge.js', 'internal/everything/store.go', 'internal/everything/handler.go', 'internal/everything/static.go'):
            self.assertEqual((ROOT / rel).read_bytes(), (ROOT / 'images' / 'everything' / rel).read_bytes(), rel)

    def test_catalog_is_closed_by_default_path_served_and_not_in_the_signed_manifest(self):
        y = (ROOT / 'catalog/three-d.yaml').read_text()
        for needle in ('serve: path', 'adapter: env-oidc', 'exclusive: true', 'HOMESTEAD_EVERYTHING_AWAIT_SIGNIN: "1"',
                       'image: homestead/everything-three-d:0.1.0', 'backuppaths:', 'path: data/bridge.db', 'icon: mdi:vector-curve', 'Chromium'):
            self.assertIn(needle, y)
        self.assertNotIn('everything-three-d', (ROOT / 'catalog/craft/craft-manifest.yaml').read_text())

    def test_bookkeeping(self):
        self.assertTrue((ROOT / 'docs/slices/S377-three-d.md').is_file())
        self.assertRegex((ROOT / 'docs/SLICE_INDEX.md').read_text(), r'(?m)^S377 ')
        self.assertIn('S377', (ROOT / 'docs/reviews/EVERYTHING_REVIEW_PLAN.md').read_text())
        third = (ROOT / 'docs/THIRD_PARTY.md').read_text()
        self.assertIn('## 3D source (S377', third)
        self.assertIn('AGPL-3.0-only', third)
        deferred = (ROOT / 'docs/DEFERRED.md').read_text()
        self.assertIn('- S377:', deferred)
        for line in deferred.splitlines():
            if line.startswith('- S377:'):
                self.assertNotIn('homesteadctl', line)


if __name__ == '__main__':
    unittest.main()
