"""S376: Parametric as an Everything catalog package (ADR-057)."""
import hashlib
import importlib.util
import json
import re
import tarfile
import tempfile
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
DEF = ROOT / 'scripts/everything/apps/parametric.json'
ARCHIVE = ROOT / 'third_party/everything/parametric-source.tar.gz'
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


class ParametricPackageTest(unittest.TestCase):
    def test_definition_archive_and_pin_agree(self):
        d = json.loads(DEF.read_text())
        data = ARCHIVE.read_bytes()
        self.assertEqual(d['sha256'], hashlib.sha256(data).hexdigest())
        self.assertEqual(d['size'], len(data))
        pin = pin_document('parametric')
        self.assertEqual(pin['sha256'], d['sha256'])
        self.assertEqual(pin['archive'], d['archive'])
        self.assertEqual(pin['license'], 'MIT')
        self.assertEqual(pin['commit'], pin_document('tasks')['commit'])
        self.assertIn('version: ' + d['version'], (ROOT / 'catalog/parametric.yaml').read_text())

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
                self.assertTrue(m.name in {'LICENSE', 'dev/puter-localhost.mjs'} or m.name.startswith('src/parametric/'), m.name)
                self.assertFalse(set(Path(m.name).parts) & {'node_modules', 'dist', '..'}, m.name)
            self.assertTrue(tar.extractfile('LICENSE').read().decode().startswith('MIT License'))
            self.assertTrue(tar.extractfile('src/parametric/LICENSE').read().decode().startswith('MIT License'))  # the app's own licence
            self.assertIn('src/parametric/public/licenses/replicad-opencascadejs-LICENSE.txt', names)  # the LGPL text travels with the app
            index = tar.extractfile('src/parametric/index.html').read()
            self.assertEqual(index.count(b'https://js.puter.com/v2/'), 1)
            self.assertNotIn(b'plausible', index)  # this app ships no analytics script
            self.assertEqual(index.count(b'parametric.puter.com'), 3)  # link-preview addresses, removed by patches
            pkg = json.loads(tar.extractfile('src/parametric/package.json').read())
            self.assertEqual(sorted(pkg['dependencies']), ['lucide', 'replicad', 'replicad-opencascadejs'])
            self.assertEqual(pkg['license'], 'MIT')
            self.assertFalse([n for n in names if n.endswith('.wasm')])  # the kernel module comes from npm at build time

    def test_every_sdk_call_in_the_app_is_in_the_definition(self):
        d = json.loads(DEF.read_text())
        sdk = d['sdk_surface']
        listed = set(sdk['supported']) | set(sdk['absent'])
        calls = set()
        with tarfile.open(ARCHIVE) as tar:
            for m in tar.getmembers():
                if not m.name.startswith('src/parametric/src/') or not m.name.endswith('.ts') or m.name.startswith('src/parametric/src/formats/'):
                    continue
                text = tar.extractfile(m).read().decode()
                text = re.sub(r'/\*.*?\*/', '', text, flags=re.S)
                text = re.sub(r'(?m)^\s*//.*$', '', text)
                # The app reaches the SDK as puter().x, p.x, sdk.x or sdk?.x
                # (src/platform/puter.ts, files.ts and src/app/share.ts, fileops.ts).
                for call in re.findall(r'\b(?:puter\(\)|sdk)[!?]*\.((?:fs|kv|auth|ui)[!?]*\.[a-zA-Z]+)', text):
                    calls.add(re.sub(r'[!?]', '', call))
                # Aliases: const fs = puter()?.fs; fs.write(...) and ui?.x, p.ui.x.
                for call in re.findall(r'\bfs[!?]*\.(write|read|readdir|delete|rename|stat|mkdir|share|getShares|unshare|listShared)\b', text):
                    calls.add('fs.' + call)
                for call in re.findall(r'\bui[!?]*\.(showOpenFilePicker|showSaveFilePicker|onLaunchedWithItems|wasLaunchedWithItems|setWindowTitle|onWindowClose)', text):
                    calls.add('ui.' + call)
        self.assertTrue({'fs.read', 'fs.write', 'fs.delete', 'fs.readdir', 'fs.rename', 'kv.get', 'kv.set'} <= calls)
        for call in calls:
            self.assertIn(call, listed, 'unlisted SDK call ' + call)
        self.assertFalse(calls & {'ai.chat', 'net.fetch'})
        shim = (ROOT / 'scripts/everything/puter-bridge.js').read_text()
        self.assertNotRegex(shim, r'(?m)^\s*ui\s*[:=]')  # ui.* stays absent
        # File sharing stays absent from fs until S429 (kv.share exists since S428).
        fs_block = shim[shim.index('var fs = {'):shim.index('var user = null;')]
        for refused in ('share', 'getShares', 'unshare', 'listShared', 'getProfilePicture'):
            self.assertNotRegex(fs_block, r'(?m)^\s*(async\s+)?' + refused + r'\s*[(:]')
        self.assertRegex(shim, r'(?m)^\s*async rename\(')  # S376
        # Sharing and the Puter file picker are hidden, not left to error.
        self.assertTrue(any("id: 'share'" in p['old'] for p in d['patches']))
        self.assertTrue(any("m('share')" in p['old'] for p in d['patches']))
        self.assertTrue(any('const shared' in p['old'] for p in d['patches']))

    def test_the_kernel_wasm_is_shipped_so_the_policy_allows_it(self):
        go = (ROOT / 'internal/everything/static.go').read_text()
        self.assertIn("'wasm-unsafe-eval'", go)  # reused from S375, not duplicated
        self.assertNotIn("'unsafe-eval'", go)

    def test_preparation_applies_patches(self):
        generic = load('prepare_app')
        d = generic.load_definition('parametric')
        self.assertIsNone(d['service_worker'])  # upstream ships and registers none
        self.assertEqual(len(d["patches"]), 37)
        with tempfile.TemporaryDirectory() as tmp:
            base = Path(tmp)
            (base / 'bridge.js').write_text('// bridge')
            generic.prepare_app(d, ARCHIVE, base / 'bridge.js', base / 'out')
            app = base / 'out/src/parametric'
            index = (app / 'index.html').read_text()
            for gone in ('js.puter.com', 'plausible', 'developer.puter', 'puter.com'):
                self.assertNotIn(gone, index)
            self.assertIn('./_homestead/puter-bridge.js', index)
            self.assertEqual((app / 'public/_homestead/puter-bridge.js').read_text(), '// bridge')
            self.assertFalse((app / 'public/sw.js').exists())
            for p in d['patches']:
                text = (base / 'out' / p['file']).read_text()
                if p['old'] not in p['new']:
                    self.assertNotIn(p['old'], text)
                if len(p['new']) > 20:
                    self.assertGreaterEqual(text.count(p['new']), 1)
            src = app / 'src'
            self.assertIn("{ username: 'Homestead user' }", (src / 'platform/puter.ts').read_text())
            for gone in ('github.com', 'puter.com'):
                for f in ('app/start.ts', 'app/dialogs.ts', 'app/shell.ts', 'app/actions.ts'):
                    text = (src / f).read_text().replace("export const REPO_URL = 'https://github.com/heyputer/everything';", '')
                    self.assertNotIn(gone, text, f)
            self.assertNotIn("id: 'share'", (src / 'app/actions.ts').read_text())
            self.assertNotIn("id: 'assistant'", (src / 'app/actions.ts').read_text())
            self.assertNotIn("m('share')", (src / 'app/actions.ts').read_text())
            self.assertNotIn("commandButton(cmds, 'assistant')", (src / 'app/shell.ts').read_text())
            self.assertNotIn("['assistant', 'assistant', 'Assistant']", (src / 'app/taskpane.ts').read_text())
            self.assertNotIn("taskPane.open('assistant'", (src / 'main.ts').read_text())
            self.assertIn('./licenses/THIRD_PARTY_NOTICES.txt', (src / 'app/dialogs.ts').read_text())  # the LGPL notice link stays
            # AI is off: the chat call always throws before it can reach an SDK, and the panel says why.
            ai = (src / 'platform/ai.ts').read_text()
            self.assertLess(ai.index("if (globalThis) throw new Error('AI is not available on Homestead."), ai.index('p.ai.chat(messages'))
            panel = (src / 'assistant/panel.ts').read_text()
            self.assertEqual(panel.count('AI is not available on Homestead. Nothing you write is sent anywhere.'), 2)
            # Crash recovery moves from the shared browser profile to the person's own storage.
            files = (src / 'platform/files.ts').read_text()
            self.assertIn('Recovery/${RECOVERY_FILE}', files)
            self.assertNotIn('dir.getFileHandle(RECOVERY_FILE)', files)
            self.assertNotIn('removeEntry(RECOVERY_FILE)', files)
            self.assertIn('"noUnusedLocals": false', (app / 'tsconfig.json').read_text())
            self.assertIn('"build": "tsc --noEmit && vite build"', (app / 'package.json').read_text())  # the typecheck stays on
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
        for rel in ('scripts/everything/apps/parametric.json', 'scripts/everything/puter-bridge.js',
                    'internal/everything/store.go', 'internal/everything/handler.go', 'internal/everything/static.go'):
            self.assertEqual((ROOT / rel).read_bytes(), (ROOT / 'images' / 'everything' / rel).read_bytes(), rel)

    def test_catalog_is_closed_by_default_path_served_and_not_in_the_signed_manifest(self):
        y = (ROOT / 'catalog/parametric.yaml').read_text()
        for needle in ('serve: path', 'adapter: env-oidc', 'exclusive: true', 'HOMESTEAD_EVERYTHING_AWAIT_SIGNIN: "1"',
                       'image: homestead/everything-parametric:0.1.0', 'backuppaths:', 'path: data/bridge.db', 'icon: mdi:ruler-square'):
            self.assertIn(needle, y)
        self.assertNotIn('everything-parametric', (ROOT / 'catalog/craft/craft-manifest.yaml').read_text())

    def test_bookkeeping(self):
        self.assertTrue((ROOT / 'docs/slices/S376-parametric.md').is_file())
        self.assertRegex((ROOT / 'docs/SLICE_INDEX.md').read_text(), r'(?m)^S376 ')
        self.assertIn('S376', (ROOT / 'docs/reviews/EVERYTHING_REVIEW_PLAN.md').read_text())
        self.assertIn('## Parametric source (S376', (ROOT / 'docs/THIRD_PARTY.md').read_text())
        self.assertIn('LGPL-2.1', (ROOT / 'docs/THIRD_PARTY.md').read_text())
        deferred = (ROOT / 'docs/DEFERRED.md').read_text()
        self.assertIn('- S376:', deferred)
        for line in deferred.splitlines():
            if line.startswith('- S376:'):
                self.assertNotIn('homesteadctl', line)


if __name__ == '__main__':
    unittest.main()
