"""S423: the in-app source offer for Everything apps with copyleft code."""
import filecmp
import hashlib
import importlib.util
import json
from pathlib import Path
import re
import subprocess
import sys
import tarfile
import tempfile
import unittest

ROOT = Path(__file__).resolve().parents[1]
APPS = ROOT / 'scripts/everything/apps'
OFFER_APPS = {'notes', 'three-d', 'parametric'}


def definitions():
    return {p.stem: json.loads(p.read_text()) for p in sorted(APPS.glob('*.json'))}


def make(app, out):
    spec = importlib.util.spec_from_file_location('make_source_offer', ROOT / 'scripts/everything/make_source_offer.py')
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    d = definitions()[app]
    return module.make(app, ROOT / 'third_party/everything' / d['archive'], ROOT / 'images/everything/Dockerfile', out)


class SourceOfferTest(unittest.TestCase):
    def test_exactly_three_apps_declare_an_offer(self):
        declared = {a for a, d in definitions().items() if d.get('source_offer')}
        self.assertEqual(declared, OFFER_APPS)

    def test_declarations_match_the_pin_and_the_licences(self):
        pins = {}
        for doc in (ROOT / 'third_party/everything/pin.yaml').read_text().split('\n---\n'):
            fields = dict(re.findall(r'^(\w+): (.+)$', doc, re.M))
            if 'app' in fields:
                pins[fields['app']] = fields
        for app in OFFER_APPS:
            so = definitions()[app]['source_offer']
            self.assertEqual(so['commit'], pins[app]['commit'])
            self.assertEqual(so['repository'], pins[app]['repository'])
            self.assertEqual(so['license'], pins[app]['license'])
            self.assertTrue(so['repository'].startswith('https://'))
        for app in ('notes', 'three-d'):
            so = definitions()[app]['source_offer']
            self.assertEqual(so['license'], 'AGPL-3.0-only')
            self.assertTrue(so['tarball'])
        par = definitions()['parametric']['source_offer']
        self.assertFalse(par['tarball'])
        [component] = par['components']
        self.assertEqual(component['license'], 'LGPL-2.1-only')
        self.assertTrue(component['source_url'].startswith('https://'))
        with tarfile.open(ROOT / 'third_party/everything/parametric-source.tar.gz') as tar:
            lock = json.load(tar.extractfile('src/parametric/package-lock.json'))
        locked = lock['packages']['node_modules/replicad-opencascadejs']
        self.assertEqual((locked['version'], locked['license']), (component['version'], component['license']))
        # The licence text it points to is shipped in the app's public/ files.
        self.assertTrue(component['license_file'].startswith('licenses/'))

    def test_apps_without_a_declaration_get_nothing(self):
        with tempfile.TemporaryDirectory() as tmp:
            out = Path(tmp) / 'offer'
            self.assertFalse(make('tasks', out))
            self.assertEqual(list(out.iterdir()), [])

    def test_tarball_is_reproducible_complete_and_rebuilds_the_prepared_tree(self):
        for app in ('notes', 'three-d'):
            d = definitions()[app]
            with tempfile.TemporaryDirectory() as tmp:
                t = Path(tmp)
                self.assertTrue(make(app, t / 'a'))
                self.assertTrue(make(app, t / 'b'))
                tarball = f'{app}-source.tar.gz'
                self.assertEqual((t / 'a' / tarball).read_bytes(), (t / 'b' / tarball).read_bytes())
                offer = json.loads((t / 'a/offer.json').read_text())
                data = (t / 'a' / tarball).read_bytes()
                self.assertEqual(offer['tarball'], {'name': tarball, 'size': len(data), 'sha256': hashlib.sha256(data).hexdigest()})
                self.assertLess(len(data), 16 * 1024 * 1024)
                self.assertEqual(offer['commit'], d['source_offer']['commit'])
                ex = t / 'x'
                ex.mkdir()
                with tarfile.open(t / 'a' / tarball) as tar:
                    names = tar.getnames()
                    self.assertEqual(names, sorted(names))
                    for m in tar.getmembers():
                        self.assertEqual((m.uid, m.gid, m.mtime), (0, 0, 0))
                    tar.extractall(ex, filter='data')
                root = ex / f'{app}-source'
                self.assertEqual((root / 'artifact' / d['archive']).read_bytes(), (ROOT / 'third_party/everything' / d['archive']).read_bytes())
                for a in d.get('add_files', []):
                    self.assertTrue((root / 'scripts/everything/files' / a['source']).is_file())
                self.assertTrue((root / 'README.txt').is_file())
                self.assertTrue((root / 'Dockerfile').is_file())
                # The README's one command, run from the tarball alone, gives the
                # same prepared tree as the build does.
                subprocess.run([sys.executable, '-I', 'scripts/everything/prepare_app.py', app, f'artifact/{d["archive"]}',
                                'scripts/everything/puter-bridge.js', str(t / 'prepared')], cwd=root, check=True)
                direct = t / 'direct'
                subprocess.run([sys.executable, '-I', str(ROOT / 'scripts/everything/prepare_app.py'), app,
                                str(ROOT / 'third_party/everything' / d['archive']),
                                str(ROOT / 'scripts/everything/puter-bridge.js'), str(direct)], check=True)
                cmp = filecmp.dircmp(t / 'prepared', direct)
                self.assertEqual((cmp.left_only, cmp.right_only, cmp.diff_files), ([], [], []))
                self.assertTrue(filecmp.cmpfiles(t / 'prepared' / d['subtree'], direct / d['subtree'], ['index.html'], shallow=False)[0])

    def test_image_recipe_ships_the_offer_and_the_context_copy_matches(self):
        docker = (ROOT / 'images/everything/Dockerfile').read_text()
        self.assertIn('make_source_offer.py', docker)
        self.assertIn('COPY --from=prepare /offer/ /out/srv/source/', docker)
        self.assertIn('HOMESTEAD_EVERYTHING_SOURCE_DIR=/srv/source', docker)
        self.assertTrue(filecmp.cmp(ROOT / 'scripts/everything/make_source_offer.py',
                                    ROOT / 'images/everything/scripts/everything/make_source_offer.py', shallow=False))

    def test_old_deferred_lines_are_closed(self):
        text = (ROOT / 'docs/DEFERRED.md').read_text()
        self.assertNotIn('S371: AGPL section 13', text)
        self.assertNotIn('no in-app source link is built', text)
        self.assertNotIn('Owner should confirm the LGPL terms', text)
        self.assertNotIn('homesteadctl', text.split('- S423:')[1])


if __name__ == '__main__':
    unittest.main()
