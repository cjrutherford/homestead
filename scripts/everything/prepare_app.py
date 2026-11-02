#!/usr/bin/env python3
"""Prepare a pinned Everything app source tree for a bridge build (ADR-057).

Generic over apps: everything app-specific lives in scripts/everything/apps/
<app>.json (archive digest, source subtree, SDK tag, patches, service-worker
template, SDK surface). Adding an app adds a JSON file and a pinned archive;
this script does not change. Nothing is fetched, and nothing from the archive
is executed. Every patch target must occur exactly once or preparation fails
with no output.
"""
import argparse
import hashlib
import io
import json
from pathlib import Path, PurePosixPath
import tarfile

HERE = Path(__file__).resolve().parent
MAX_FILE = 16 * 1024 * 1024
MAX_TOTAL = 64 * 1024 * 1024
APP_RE = __import__('re').compile(r'^[a-z][a-z0-9-]*$')


def load_definition(app: str) -> dict:
    if not APP_RE.match(app):
        raise ValueError('invalid app name')
    path = HERE / 'apps' / (app + '.json')
    d = json.loads(path.read_text())
    if d.get('app') != app:
        raise ValueError('app definition does not name ' + app)
    for key in ('archive', 'sha256', 'subtree', 'index', 'sdk_tag', 'patches', 'required', 'service_worker', 'sdk_surface'):
        if key not in d:
            raise ValueError('app definition lacks ' + key)
    return d


def prepare_app(definition: dict, archive_path: Path, bridge_path: Path, output: Path, expected_sha256: str = '') -> None:
    d = definition
    sub = d['subtree'].rstrip('/') + '/'
    extra = set(d.get('extra_files', []))
    sdk_tag = d['sdk_tag'].encode()
    local_tag = b'<script src="./_homestead/puter-bridge.js" defer></script>'
    data = archive_path.read_bytes()
    if hashlib.sha256(data).hexdigest() != (expected_sha256 or d['sha256']):
        raise ValueError('source archive digest mismatch')
    bridge = bridge_path.read_bytes()
    if not bridge or len(bridge) > MAX_FILE:
        raise ValueError('bridge is empty or too large')
    if output.exists():
        raise ValueError('output already exists')
    worker = (HERE / d['service_worker']['template']).read_bytes()
    if b'__VERSION__' not in worker or b'__ASSETS__' not in worker:
        raise ValueError('invalid local service worker template')
    patches = {}
    for p in d['patches']:
        patches.setdefault(p['file'], []).append((p['old'].encode(), p['new'].encode()))
    with tarfile.open(fileobj=io.BytesIO(data), mode='r:gz') as archive:
        members = archive.getmembers()
        names = set()
        total = 0
        for member in members:
            name = PurePosixPath(member.name)
            if (not member.isfile() or name.is_absolute() or '..' in name.parts or
                    '\\' in member.name or name.as_posix() != member.name or
                    member.name in names or member.size > MAX_FILE or
                    not (member.name in extra or member.name.startswith(sub))):
                raise ValueError('unsafe or unexpected source member: ' + member.name)
            names.add(member.name)
            total += member.size
        if total > MAX_TOTAL:
            raise ValueError('source archive too large')
        if not set(d['required']).issubset(names):
            raise ValueError('source archive missing required files')
        index = archive.extractfile(d['index']).read()
        if index.count(sdk_tag) != 1:
            raise ValueError('expected exactly one upstream SDK tag')
        for patched, edits in patches.items():
            if patched not in names:
                raise ValueError('UI patch target changed in ' + patched)
            body = archive.extractfile(patched).read()
            for old, _new in edits:
                if body.count(old) != 1:
                    raise ValueError('UI patch target changed in ' + patched)
        output.mkdir(parents=True)
        for member in members:
            destination = output.joinpath(*PurePosixPath(member.name).parts)
            destination.parent.mkdir(parents=True, exist_ok=True)
            content = archive.extractfile(member).read()
            if member.name == d['index']:
                content = content.replace(sdk_tag, local_tag)
            if member.name == d['service_worker']['target']:
                content = worker
            for old, new in patches.get(member.name, []):
                content = content.replace(old, new)
            destination.write_bytes(content)
        target = output / sub / 'public/_homestead/puter-bridge.js'
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_bytes(bridge)


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('app')
    parser.add_argument('archive', type=Path)
    parser.add_argument('bridge', type=Path)
    parser.add_argument('output', type=Path)
    parser.add_argument('--print-subtree', action='store_true', help='print the app source subtree and exit')
    args = parser.parse_args()
    d = load_definition(args.app)
    if args.print_subtree:
        print(d['subtree'])
        return
    prepare_app(d, args.archive, args.bridge, args.output)


if __name__ == '__main__':
    main()
