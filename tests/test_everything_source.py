"""Integrity checks for the S367 Tasks source snapshot, not bridge acceptance."""
import hashlib
import io
import importlib.util
import tempfile
from pathlib import Path
import tarfile
import unittest

ROOT = Path(__file__).resolve().parents[1]
PIN = ROOT / 'third_party/everything/pin.yaml'
ARCHIVE = ROOT / 'third_party/everything/tasks-source.tar.gz'


class EverythingSourceTest(unittest.TestCase):
    def test_preparation_replaces_only_the_sdk_with_the_supplied_bridge(self):
        spec = importlib.util.spec_from_file_location('prepare_tasks', ROOT / 'scripts/everything/prepare_tasks.py')
        module = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(module)
        with tempfile.TemporaryDirectory() as tmp:
            base = Path(tmp)
            bridge = base / 'bridge.js'
            bridge.write_text('// test bridge')
            output = base / 'prepared'
            module.prepare(ARCHIVE, bridge, output)
            index = (output / 'src/tasks/index.html').read_bytes()
            self.assertNotIn(b'https://js.puter.com/v2/', index)
            self.assertIn(b'./_homestead/puter-bridge.js', index)
            self.assertEqual((output / 'src/tasks/public/_homestead/puter-bridge.js').read_text(), '// test bridge')
            with self.assertRaisesRegex(ValueError, 'output already exists'):
                module.prepare(ARCHIVE, bridge, output)
            bad = base / 'bad.tar.gz'
            bad.write_bytes(ARCHIVE.read_bytes() + b'corrupt')
            with self.assertRaisesRegex(ValueError, 'digest mismatch'):
                module.prepare(bad, bridge, base / 'rejected')
            self.assertFalse((base / 'rejected').exists())

    def test_preparation_rejects_nonportable_archive_paths(self):
        spec = importlib.util.spec_from_file_location('prepare_tasks', ROOT / 'scripts/everything/prepare_tasks.py')
        module = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(module)
        with tempfile.TemporaryDirectory() as tmp:
            base = Path(tmp)
            archive = base / 'malicious.tar.gz'
            with tarfile.open(archive, 'w:gz') as tar:
                member = tarfile.TarInfo('src/tasks/..\\escape')
                member.size = 1
                tar.addfile(member, io.BytesIO(b'x'))
            module.EXPECTED_SHA256 = hashlib.sha256(archive.read_bytes()).hexdigest()
            bridge = base / 'bridge.js'
            bridge.write_text('// bridge')
            with self.assertRaisesRegex(ValueError, 'unsafe or unexpected'):
                module.prepare(archive, bridge, base / 'output')
            self.assertFalse((base / 'output').exists())

    def test_pinned_source_is_safe_and_matches_digest(self):
        metadata = dict(line.split(': ', 1) for line in PIN.read_text().splitlines()
                        if line and not line.startswith('#'))
        self.assertEqual(metadata['repository'], 'https://github.com/HeyPuter/everything')
        self.assertEqual(metadata['commit'], 'e7d07c48546ef0650a8f3ab94ae82b8b50d3646a')
        data = ARCHIVE.read_bytes()
        self.assertEqual(hashlib.sha256(data).hexdigest(), metadata['sha256'])
        with tarfile.open(fileobj=io.BytesIO(data), mode='r:gz') as archive:
            members = archive.getmembers()
            names = [member.name for member in members]
            self.assertEqual(len(names), len(set(names)))
            self.assertIn('LICENSE', names)
            self.assertIn('dev/puter-localhost.mjs', names)
            self.assertIn('src/tasks/package-lock.json', names)
            self.assertIn('src/tasks/index.html', names)
            for member in members:
                self.assertTrue(member.isfile(), member.name)
                self.assertFalse(member.name.startswith('/'), member.name)
                self.assertNotIn('..', Path(member.name).parts)
                self.assertNotIn('node_modules', Path(member.name).parts)
                self.assertNotIn('dist', Path(member.name).parts)
                self.assertTrue(member.name in {'LICENSE', 'dev/puter-localhost.mjs'} or member.name.startswith('src/tasks/'))
            index = archive.extractfile('src/tasks/index.html').read()
            # Preserve upstream bytes; S367 still needs to replace this with a local bridge.
            self.assertEqual(index.count(b'https://js.puter.com/v2/'), 1)


if __name__ == '__main__':
    unittest.main()
