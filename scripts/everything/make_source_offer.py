#!/usr/bin/env python3
"""Build the source offer an Everything image serves (S423, ADR-057).

usage: make_source_offer.py APP ARCHIVE DOCKERFILE OUTPUT_DIR

An app whose definition (scripts/everything/apps/<app>.json) has no
"source_offer" gets an empty OUTPUT_DIR. Otherwise OUTPUT_DIR gets offer.json
(what the bridge's /_homestead/source page shows) and, when the declaration
asks for "tarball", <app>-source.tar.gz: the pinned upstream archive, verbatim,
plus everything Homestead adds to it, laid out so the README's one command
rebuilds the prepared source. The tarball is deterministic (sorted names, root
owner, mtime 0, gzip without a timestamp), so the same inputs give the same
bytes. Nothing is fetched.
"""
import gzip
import hashlib
import importlib.util
import io
import json
from pathlib import Path
import sys
import tarfile

HERE = Path(__file__).resolve().parent
README = """Corresponding Source for {title} as served by Homestead (S423)
============================================================

Licence: {license}
Upstream: {repository}
Pinned commit: {commit}
Path in the repository: {path}

What is here
  artifact/{archive}
      The pinned upstream source archive, byte for byte
      (SHA-256 {sha}).
  scripts/everything/apps/{app}.json
      The patch list: every edit Homestead makes to the upstream source,
      as exact old/new text.
  scripts/everything/files/
      Source files Homestead adds to the tree (if the app needs any).
  scripts/everything/puter-bridge.js
      The browser shim that replaces the hosted Puter SDK.
  scripts/everything/*-sw.template.js
      The offline-page worker template (if the app has one).
  scripts/everything/prepare_app.py
      The preparation script that applies all of the above.
  Dockerfile
      The image recipe that built the running version.

To rebuild the prepared source tree (needs Python 3):
  python3 -I scripts/everything/prepare_app.py {app} artifact/{archive} scripts/everything/puter-bridge.js prepared
Then, in prepared/{path}:
  npm ci --ignore-scripts && npm run build
The result in dist/ is what the bridge serves.
"""


def load(name: str):
    spec = importlib.util.spec_from_file_location(name, HERE / (name + '.py'))
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def build_tarball(app: str, d: dict, archive: Path, dockerfile: Path, out: Path) -> dict:
    entries = {
        f'artifact/{d["archive"]}': archive.read_bytes(),
        f'scripts/everything/apps/{app}.json': (HERE / 'apps' / (app + '.json')).read_bytes(),
        'scripts/everything/puter-bridge.js': (HERE / 'puter-bridge.js').read_bytes(),
        'scripts/everything/prepare_app.py': (HERE / 'prepare_app.py').read_bytes(),
        'Dockerfile': dockerfile.read_bytes(),
    }
    for a in d.get('add_files', []):
        entries['scripts/everything/files/' + a['source']] = (HERE / 'files' / a['source']).read_bytes()
    if d['service_worker']:
        entries['scripts/everything/' + d['service_worker']['template']] = (HERE / d['service_worker']['template']).read_bytes()
    so = d['source_offer']
    entries['README.txt'] = README.format(
        title=d['title'], license=so['license'], repository=so['repository'], commit=so['commit'],
        path=d['subtree'], archive=d['archive'], sha=d['sha256'], app=app).encode()
    root = f'{app}-source/'
    raw = io.BytesIO()
    with gzip.GzipFile(fileobj=raw, mode='wb', mtime=0, compresslevel=9, filename='') as gz:
        with tarfile.open(fileobj=gz, mode='w', format=tarfile.GNU_FORMAT) as tar:
            for name in sorted(entries):
                info = tarfile.TarInfo(root + name)
                info.size = len(entries[name])
                info.mode = 0o644
                info.uid = info.gid = 0
                info.uname = info.gname = ''
                info.mtime = 0
                tar.addfile(info, io.BytesIO(entries[name]))
    data = raw.getvalue()
    (out / f'{app}-source.tar.gz').write_bytes(data)
    return {'name': f'{app}-source.tar.gz', 'size': len(data), 'sha256': hashlib.sha256(data).hexdigest()}


def make(app: str, archive: Path, dockerfile: Path, out: Path) -> bool:
    d = load('prepare_app').load_definition(app)
    so = d.get('source_offer')
    out.mkdir(parents=True, exist_ok=True)
    if not so:
        return False
    if hashlib.sha256(archive.read_bytes()).hexdigest() != d['sha256']:
        raise ValueError('source archive digest mismatch')
    offer = {
        'app': app, 'title': d['title'], 'license': so['license'], 'license_url': so.get('license_url', ''),
        'repository': so['repository'], 'commit': so['commit'], 'path': d['subtree'],
        'tarball': build_tarball(app, d, archive, dockerfile, out) if so.get('tarball') else None,
        'upstream_archive': {'name': d['archive'], 'sha256': d['sha256']} if so.get('tarball') else None,
        'components': so.get('components', []),
        'link_offset': so.get('link_offset', {}),
    }
    (out / 'offer.json').write_text(json.dumps(offer, indent=2, sort_keys=True) + '\n')
    return True


def main() -> None:
    if len(sys.argv) != 5:
        sys.exit(__doc__)
    make(sys.argv[1], Path(sys.argv[2]), Path(sys.argv[3]), Path(sys.argv[4]))


if __name__ == '__main__':
    main()
