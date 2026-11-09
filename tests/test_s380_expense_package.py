"""S380: Expense as an Everything catalog package (ADR-057)."""
import hashlib
import importlib.util
import json
import re
import tarfile
import tempfile
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
DEF = ROOT / 'scripts/everything/apps/expense.json'
ARCHIVE = ROOT / 'third_party/everything/expense-source.tar.gz'
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


class ExpensePackageTest(unittest.TestCase):
    def test_definition_archive_and_pin_agree(self):
        d = json.loads(DEF.read_text())
        data = ARCHIVE.read_bytes()
        self.assertEqual(d['sha256'], hashlib.sha256(data).hexdigest())
        self.assertEqual(d['size'], len(data))
        pin = pin_document('expense')
        self.assertEqual(pin['sha256'], d['sha256'])
        self.assertEqual(pin['archive'], d['archive'])
        self.assertEqual(pin['license'], 'MIT')
        self.assertEqual(pin['commit'], pin_document('tasks')['commit'])
        self.assertIn('version: ' + d['version'], (ROOT / 'catalog/expense.yaml').read_text())

    def test_archive_is_plain_files_of_the_app_only_and_mit(self):
        with tarfile.open(ARCHIVE) as tar:
            members = tar.getmembers()
            names = [m.name for m in members]
            self.assertEqual(names, sorted(names))
            self.assertEqual(len(names), len(set(names)))
            self.assertEqual(len(names), 68)
            for m in members:
                self.assertTrue(m.isfile(), m.name)
                self.assertEqual((m.uid, m.gid, m.mtime), (0, 0, 0), m.name)
                self.assertTrue(m.name in {'LICENSE', 'dev/puter-localhost.mjs'} or m.name.startswith('src/expense/'), m.name)
                self.assertFalse(set(Path(m.name).parts) & {'node_modules', 'dist', '..'}, m.name)
            self.assertTrue(tar.extractfile('LICENSE').read().decode().startswith('MIT License'))
            self.assertNotIn('src/expense/LICENSE', names)  # the app has no licence file of its own
            pkg = json.loads(tar.extractfile('src/expense/package.json').read())
            self.assertEqual(set(pkg['dependencies']), {'lucide'})  # ISC icons, served locally

    def test_sdk_surface_is_what_the_app_uses(self):
        d = json.loads(DEF.read_text())
        sdk = d['sdk_surface']
        calls = set()
        with tarfile.open(ARCHIVE) as tar:
            for m in tar.getmembers():
                if m.name.startswith('src/expense/src/') and m.name.endswith('.js'):
                    calls |= set(re.findall(r'\b((?:auth|kv|fs|ui|ai|net)\.[a-zA-Z0-9]+)\(', tar.extractfile(m).read().decode()))
        self.assertEqual(calls, {'auth.isSignedIn', 'auth.getUser', 'auth.signIn', 'auth.signOut', 'kv.get', 'kv.set',
                                 'fs.read', 'fs.write', 'fs.delete', 'ai.chat', 'ai.img2txt'})
        # sign-in and sign-out are unreachable (env "app" hides sign-out); the ai calls are fail-closed by patch and the shim has no ai
        self.assertEqual(set(sdk['supported']), calls - {'auth.signIn', 'auth.signOut', 'ai.chat', 'ai.img2txt'})
        self.assertEqual(set(sdk['disabled']), {'ai', 'net', 'sharing'})
        shim = (ROOT / 'scripts/everything/puter-bridge.js').read_text()
        self.assertNotRegex(shim, r'(?m)^\s*(ai|net)\s*:')
        self.assertRegex(shim, r'(?m)^\s*async delete\(path, opts\)')

    def test_preparation_applies_patches_and_the_entry_point(self):
        generic = load('prepare_app')
        d = generic.load_definition('expense')
        self.assertEqual(len(d['patches']), 37)
        AI = 'AI is not available on Homestead. Nothing you write is sent anywhere.'
        with tempfile.TemporaryDirectory() as tmp:
            base = Path(tmp)
            (base / 'bridge.js').write_text('// bridge')
            generic.prepare_app(d, ARCHIVE, base / 'bridge.js', base / 'out')
            app = base / 'out/src/expense'
            index = (app / 'index.html').read_text()
            self.assertLess(index.index('./_homestead/puter-bridge.js'), index.index('/src/homestead/boot.js'))
            self.assertNotIn('/src/main.js', index)
            self.assertNotRegex(index, r'puter\.com|AI OCR')
            self.assertNotRegex((app / 'public/manifest.webmanifest').read_text(), r'puter|AI OCR')
            self.assertEqual((app / 'src/homestead/boot.js').read_text(), (ROOT / 'scripts/everything/files/expense-person-storage.js').read_text())
            # upstream's worker (which cached index.html and the hosted SDK) is replaced by Homestead's
            sw = (app / 'public/sw.js').read_text()
            self.assertIn("'homestead-everything-expense-'", sw)
            self.assertNotIn('js.puter.com', sw)
            self.assertNotIn('__VERSION__', sw)
            boot = (app / 'src/homestead/boot.js').read_text()
            for needle in ('auth.getUser', "'hs:'", 'memoryStorage', "import('../main.js')", "register('./sw.js')"):
                self.assertIn(needle, boot)
            # no visible Puter wording or link, and no reading of receipts by AI, in any shipped source
            shown = ''
            for f in (app / 'src').rglob('*.js'):
                if 'lib/ocr.js' in f.as_posix() or 'lib/store.js' in f.as_posix():
                    continue
                shown += f.read_text()
            strings = ' '.join(re.findall(r"'[^'\n]*'|\"[^\"\n]*\"|`[^`\n]*`", shown))  # string literals: what can be shown
            self.assertNotRegex(strings, r"Puter|developer\.puter|AI OCR|let AI|with AI|Skip AI|AI receipt|Vision model")
            # AI fails closed before anything is built or sent, and a capture never starts a reading
            ocr = (app / 'src/lib/ocr.js').read_text()
            self.assertIn("if (!sdk || !sdk.ai) throw new Error('" + AI + "');", ocr)
            self.assertLess(ocr.index(AI), ocr.index('buildOcrPrompt({ categories, baseCurrency })'))
            capture = (app / 'src/ui/capture.js').read_text()
            self.assertIn('    skip.click(); return;\n', capture)
            self.assertIn('MAX_BYTES = 8 * 1024 * 1024', capture)
            self.assertNotIn('10 MB', capture)
            # the account is "Homestead user", never the subject
            for rel in ('src/ui/shell.js', 'src/views/settings.js', 'src/views/dashboard.js'):
                self.assertNotRegex((app / rel).read_text(), r'store\.user\??\.(username|email)')
            self.assertIn("recursive: true", (app / 'src/lib/store.js').read_text())

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
        for rel in ('scripts/everything/apps/expense.json', 'scripts/everything/files/expense-person-storage.js',
                    'scripts/everything/expense-sw.template.js'):
            self.assertEqual((ROOT / rel).read_bytes(), (ROOT / 'images' / 'everything' / rel).read_bytes(), rel)

    def test_catalog_is_closed_by_default_path_served_and_not_in_the_signed_manifest(self):
        y = (ROOT / 'catalog/expense.yaml').read_text()
        for needle in ('serve: path', 'adapter: env-oidc', 'exclusive: true', 'HOMESTEAD_EVERYTHING_AWAIT_SIGNIN: "1"',
                       'image: homestead/everything-expense:0.1.0', 'backuppaths:', 'path: data/bridge.db', 'icon: mdi:piggy-bank-outline'):
            self.assertIn(needle, y)
        # no receipt-reading claim in the catalog copy
        self.assertNotRegex(y, r'(?i)\bocr\b|read(s|ing)? (your )?receipts? (with|by) ai|let ai')
        self.assertNotIn('everything-expense', (ROOT / 'catalog/craft/craft-manifest.yaml').read_text())

    def test_bookkeeping(self):
        self.assertTrue((ROOT / 'docs/slices/S380-expense.md').is_file())
        self.assertRegex((ROOT / 'docs/SLICE_INDEX.md').read_text(), r'(?m)^S380 ')
        self.assertIn('S380', (ROOT / 'docs/reviews/EVERYTHING_REVIEW_PLAN.md').read_text())
        self.assertIn('## Expenses source (S380', (ROOT / 'docs/THIRD_PARTY.md').read_text())
        deferred = (ROOT / 'docs/DEFERRED.md').read_text()
        self.assertIn('- S380:', deferred)
        for line in deferred.splitlines():
            if line.startswith('- S380:'):
                self.assertNotIn('homesteadctl', line)


if __name__ == '__main__':
    unittest.main()
