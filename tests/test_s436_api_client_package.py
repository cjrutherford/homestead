"""S436: API Client as an Everything catalog package using net.fetch (ADR-057, ADR-058)."""
import hashlib
import importlib.util
import json
import re
import tarfile
import tempfile
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
DEF = ROOT / 'scripts/everything/apps/api-client.json'
ARCHIVE = ROOT / 'third_party/everything/api-client-source.tar.gz'
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


class ApiClientPackageTest(unittest.TestCase):
    def test_definition_archive_and_pin_agree(self):
        d = json.loads(DEF.read_text())
        data = ARCHIVE.read_bytes()
        self.assertEqual(d['sha256'], hashlib.sha256(data).hexdigest())
        self.assertEqual(d['size'], len(data))
        pin = pin_document('api-client')
        self.assertEqual(pin['sha256'], d['sha256'])
        self.assertEqual(pin['archive'], d['archive'])
        self.assertEqual(pin['license'], 'MIT')
        self.assertEqual(pin['commit'], pin_document('tasks')['commit'])
        self.assertIn('version: ' + d['version'], (ROOT / 'catalog/api-client.yaml').read_text())

    def test_archive_is_plain_files_of_the_app_only_and_mit(self):
        with tarfile.open(ARCHIVE) as tar:
            members = tar.getmembers()
            names = [m.name for m in members]
            self.assertEqual(names, sorted(names))
            self.assertEqual(len(names), len(set(names)))
            self.assertEqual(len(names), 133)
            for m in members:
                self.assertTrue(m.isfile(), m.name)
                self.assertEqual((m.uid, m.gid, m.mtime), (0, 0, 0), m.name)
                self.assertTrue(m.name in {'LICENSE', 'dev/puter-localhost.mjs'} or m.name.startswith('src/api-client/'), m.name)
                self.assertFalse(set(Path(m.name).parts) & {'node_modules', 'dist', '..'}, m.name)
            self.assertTrue(tar.extractfile('LICENSE').read().decode().startswith('MIT License'))

    def test_sdk_surface_is_what_the_app_uses(self):
        d = json.loads(DEF.read_text())
        sdk = d['sdk_surface']
        calls = set()
        with tarfile.open(ARCHIVE) as tar:
            for m in tar.getmembers():
                if m.name.startswith('src/api-client/src/') and m.name.endswith('.js') and '.test.' not in m.name:
                    calls |= set(re.findall(r'\b((?:fs|kv|ai|net)\.[a-zA-Z0-9]+)\(', tar.extractfile(m).read().decode()))
        self.assertEqual(calls, {'fs.delete', 'fs.getShares', 'fs.mkdir', 'fs.read', 'fs.readdir', 'fs.share', 'fs.unshare', 'fs.listShared', 'fs.write', 'net.Socket'})
        self.assertTrue(set(sdk['supported']) <= calls | {'auth.getUser', 'net.fetch'})
        self.assertIn('net.fetch', sdk['supported'])
        self.assertIn('net.Socket', sdk['refused'])
        self.assertEqual(set(sdk['disabled']), {'ai', 'sharing', 'websocket'})

    def test_preparation_routes_network_through_net_fetch_and_removes_the_rest(self):
        generic = load('prepare_app')
        d = generic.load_definition('api-client')
        self.assertEqual(len(d['patches']), 60)
        with tempfile.TemporaryDirectory() as tmp:
            base = Path(tmp)
            (base / 'bridge.js').write_text('// bridge')
            generic.prepare_app(d, ARCHIVE, base / 'bridge.js', base / 'out')
            app = base / 'out/src/api-client'
            index = (app / 'index.html').read_text()
            self.assertLess(index.index('./_homestead/puter-bridge.js'), index.index('/src/homestead/boot.js'))
            self.assertNotIn('/src/main.js', index)
            self.assertNotIn('js.puter.com', index)
            self.assertEqual((app / 'src/homestead/boot.js').read_text(), (ROOT / 'scripts/everything/files/api-client-person-storage.js').read_text())
            self.assertEqual((app / 'src/core/homestead-net.js').read_text(), (ROOT / 'scripts/everything/files/api-client-net.js').read_text())
            send = (app / 'src/core/send.js').read_text()
            self.assertIn('puter.net.fetch', send)
            self.assertIn("globalThis.puter?.env === 'app' ? 'relay'", send)
            self.assertIn('Raw sockets are not available on Homestead.', send)
            ws = (app / 'src/core/websocket.js').read_text()
            self.assertIn('WebSocket connections are not available on Homestead.', ws)
            self.assertIn('isMissing(err) ? []', (app / 'src/data/sync.js').read_text())
            self.assertEqual((app / 'src/main.js').read_text().count('shared = null'), 1)
            self.assertFalse((app / 'public/sw.js').exists())
            shown = ''
            for f in (app / 'src').rglob('*.js'):
                if f.name == 'share-dialog.js':
                    continue  # unreachable: sharing is hidden (shared = null)
                shown += ''.join(l for l in f.read_text().splitlines(True) if 'if (false)' not in l)  # patched-out dead code
            strings = ' '.join(re.findall(r"'[^'\n]*'|\"[^\"\n]*\"|`[^`\n]*`", shown))
            self.assertNotRegex(strings, r'developer\.puter|puter\.com|Sign in to Puter|your Puter')

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
        for rel in ('scripts/everything/apps/api-client.json', 'scripts/everything/files/api-client-person-storage.js',
                    'scripts/everything/files/api-client-net.js'):
            self.assertEqual((ROOT / rel).read_bytes(), (ROOT / 'images' / 'everything' / rel).read_bytes(), rel)

    def test_catalog_starts_the_bridge_with_net_and_is_closed_by_default(self):
        y = (ROOT / 'catalog/api-client.yaml').read_text()
        for needle in ('serve: path', 'adapter: env-oidc', 'exclusive: true', 'HOMESTEAD_EVERYTHING_NET: "1"',
                       'HOMESTEAD_EVERYTHING_AWAIT_SIGNIN: "1"', 'image: homestead/everything-api-client:0.1.0',
                       'path: data/bridge.db', 'icon: mdi:cloud'):
            self.assertIn(needle, y)
        self.assertNotIn('everything-api-client', (ROOT / 'catalog/craft/craft-manifest.yaml').read_text())
        for other in ROOT.glob('catalog/*.yaml'):
            if other.name != 'api-client.yaml':
                self.assertNotIn('HOMESTEAD_EVERYTHING_NET', other.read_text(), other.name)

    def test_bookkeeping(self):
        self.assertTrue((ROOT / 'docs/slices/S436-api-client.md').is_file())
        self.assertRegex((ROOT / 'docs/SLICE_INDEX.md').read_text(), r'(?m)^S436 ')
        self.assertIn('S436', (ROOT / 'docs/reviews/EVERYTHING_REVIEW_PLAN.md').read_text())
        self.assertIn('## API Client source (S436', (ROOT / 'docs/THIRD_PARTY.md').read_text())
        deferred = (ROOT / 'docs/DEFERRED.md').read_text()
        self.assertIn('- S436:', deferred)
        for line in deferred.splitlines():
            if line.startswith('- S436:'):
                self.assertNotIn('homesteadctl', line)


if __name__ == '__main__':
    unittest.main()
