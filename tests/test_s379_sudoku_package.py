"""S379: Sudoku as an Everything catalog package (ADR-057)."""
import hashlib
import importlib.util
import json
import re
import tarfile
import tempfile
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
DEF = ROOT / 'scripts/everything/apps/sudoku.json'
ARCHIVE = ROOT / 'third_party/everything/sudoku-source.tar.gz'
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


class SudokuPackageTest(unittest.TestCase):
    def test_definition_archive_and_pin_agree(self):
        d = json.loads(DEF.read_text())
        data = ARCHIVE.read_bytes()
        self.assertEqual(d['sha256'], hashlib.sha256(data).hexdigest())
        self.assertEqual(d['size'], len(data))
        pin = pin_document('sudoku')
        self.assertEqual(pin['sha256'], d['sha256'])
        self.assertEqual(pin['archive'], d['archive'])
        self.assertEqual(pin['license'], 'MIT')
        self.assertEqual(pin['commit'], pin_document('tasks')['commit'])
        self.assertIn('version: ' + d['version'], (ROOT / 'catalog/sudoku.yaml').read_text())

    def test_archive_is_plain_files_of_the_app_only_and_mit(self):
        with tarfile.open(ARCHIVE) as tar:
            members = tar.getmembers()
            names = [m.name for m in members]
            self.assertEqual(names, sorted(names))
            self.assertEqual(len(names), len(set(names)))
            self.assertEqual(len(names), 53)
            for m in members:
                self.assertTrue(m.isfile(), m.name)
                self.assertEqual((m.uid, m.gid, m.mtime), (0, 0, 0), m.name)
                self.assertTrue(m.name in {'LICENSE', 'dev/puter-localhost.mjs'} or m.name.startswith('src/sudoku/'), m.name)
                self.assertFalse(set(Path(m.name).parts) & {'node_modules', 'dist', '..'}, m.name)
            self.assertTrue(tar.extractfile('LICENSE').read().decode().startswith('MIT License'))
            self.assertNotIn('src/sudoku/LICENSE', names)  # the app has no licence file of its own
            pkg = json.loads(tar.extractfile('src/sudoku/package.json').read())
            self.assertEqual(set(pkg['dependencies']), {'@fontsource/inter', 'lucide'})  # OFL font and ISC icons, both served locally

    def test_sdk_surface_is_what_the_app_uses(self):
        d = json.loads(DEF.read_text())
        sdk = d['sdk_surface']
        with tarfile.open(ARCHIVE) as tar:
            sync = tar.extractfile('src/sudoku/src/net/puter.js').read().decode()
        calls = set(re.findall(r'\.((?:auth|kv|fs|ui|ai|net)\.[a-zA-Z]+)\(', sync))
        self.assertEqual(calls, {'auth.isSignedIn', 'kv.get', 'kv.set', 'kv.del', 'auth.getUser', 'auth.signIn', 'auth.signOut'})
        self.assertEqual(set(sdk['supported']), calls - {'auth.signIn', 'auth.signOut'} | {'auth.getUser'})  # the account button is a label; sign-in and sign-out are unreachable
        self.assertEqual(set(sdk['disabled']), {'ai', 'net', 'sharing'})
        shim = (ROOT / 'scripts/everything/puter-bridge.js').read_text()
        self.assertRegex(shim, r'(?m)^\s*async del\(')

    def test_preparation_applies_patches_and_the_entry_point(self):
        generic = load('prepare_app')
        d = generic.load_definition('sudoku')
        self.assertEqual(len(d['patches']), 8)
        with tempfile.TemporaryDirectory() as tmp:
            base = Path(tmp)
            (base / 'bridge.js').write_text('// bridge')
            generic.prepare_app(d, ARCHIVE, base / 'bridge.js', base / 'out')
            app = base / 'out/src/sudoku'
            index = (app / 'index.html').read_text()
            self.assertLess(index.index('./_homestead/puter-bridge.js'), index.index('/src/homestead/boot.js'))
            self.assertNotIn('/src/main.js', index)
            self.assertNotRegex(index, r'puter\.com|Powered by')
            self.assertNotRegex((app / 'public/robots.txt').read_text() + (app / 'public/site.webmanifest').read_text(), r'puter\.com')
            self.assertEqual((app / 'src/homestead/boot.js').read_text(), (ROOT / 'scripts/everything/files/sudoku-person-storage.js').read_text())
            self.assertEqual((app / 'public/sw.js').read_text(), (ROOT / 'scripts/everything/files/sudoku-sw.js').read_text())
            self.assertIn("'homestead-everything-sudoku-'", (app / 'public/sw.js').read_text())
            main = (app / 'src/main.js').read_text()
            self.assertIn("'Homestead user'", main)
            self.assertNotIn('Puter account', main)
            boot = (app / 'src/homestead/boot.js').read_text()
            for needle in ('auth.getUser', "'hs:'", 'memoryStorage', "import('../main.js')", "register('./sw.js')"):
                self.assertIn(needle, boot)

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
        for rel in ('scripts/everything/apps/sudoku.json', 'scripts/everything/files/sudoku-person-storage.js',
                    'scripts/everything/files/sudoku-sw.js'):
            self.assertEqual((ROOT / rel).read_bytes(), (ROOT / 'images' / 'everything' / rel).read_bytes(), rel)

    def test_catalog_is_closed_by_default_path_served_and_not_in_the_signed_manifest(self):
        y = (ROOT / 'catalog/sudoku.yaml').read_text()
        for needle in ('serve: path', 'adapter: env-oidc', 'exclusive: true', 'HOMESTEAD_EVERYTHING_AWAIT_SIGNIN: "1"',
                       'image: homestead/everything-sudoku:0.1.0', 'backuppaths:', 'path: data/bridge.db', 'icon: mdi:image-multiple'):
            self.assertIn(needle, y)
        self.assertNotIn('everything-sudoku', (ROOT / 'catalog/craft/craft-manifest.yaml').read_text())

    def test_bookkeeping(self):
        self.assertTrue((ROOT / 'docs/slices/S379-sudoku.md').is_file())
        self.assertRegex((ROOT / 'docs/SLICE_INDEX.md').read_text(), r'(?m)^S379 ')
        self.assertIn('S379', (ROOT / 'docs/reviews/EVERYTHING_REVIEW_PLAN.md').read_text())
        self.assertIn('## Sudoku source (S379', (ROOT / 'docs/THIRD_PARTY.md').read_text())
        deferred = (ROOT / 'docs/DEFERRED.md').read_text()
        self.assertIn('- S379:', deferred)
        for line in deferred.splitlines():
            if line.startswith('- S379:'):
                self.assertNotIn('homesteadctl', line)


if __name__ == '__main__':
    unittest.main()
