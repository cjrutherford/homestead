#!/usr/bin/env python3
"""Prepare the pinned Tasks source for a bridge build, without fetching code.

S367 is experimental: preparing source does not make the application safe or
installable. The caller must supply a reviewed local bridge and run the S367
integration and exposure gates before shipping an image.
"""
import argparse
import hashlib
import io
from pathlib import Path, PurePosixPath
import tarfile

EXPECTED_SHA256 = '864f60484a1aa759427fec5d308a2215200fa5873d7579bb73594850b8f5db2e'
SDK_TAG = b'<script src="https://js.puter.com/v2/" defer></script>'
LOCAL_TAG = b'<script src="./_homestead/puter-bridge.js" defer></script>'
# ADR-053 amendment 2026-11-02 (owner decision 2): the bridge refuses signed
# public URLs, so the Share button and the calendar-feed (ICS URL) setting are
# hidden. Each target must occur exactly once or preparation fails. The local
# "Download .ics" button builds a file in the browser and stays.
UI_PATCHES = {
    'src/tasks/src/views/lists.js': (
        b"el('button.icon-btn', {\n          html: icon('share', 18),",
        b"false && el('button.icon-btn', {\n          html: icon('share', 18),",
    ),
    'src/tasks/src/views/settings.js': (
        b"root.append(\n    el('div.section-label', {}, 'Calendar integration'),",
        b"false && root.append(\n    el('div.section-label', {}, 'Calendar integration'),",
    ),
}
MAX_FILE = 16 * 1024 * 1024
MAX_TOTAL = 64 * 1024 * 1024


def prepare(archive_path: Path, bridge_path: Path, output: Path) -> None:
    data = archive_path.read_bytes()
    if hashlib.sha256(data).hexdigest() != EXPECTED_SHA256:
        raise ValueError('source archive digest mismatch')
    bridge = bridge_path.read_bytes()
    if not bridge or len(bridge) > MAX_FILE:
        raise ValueError('bridge is empty or too large')
    if output.exists():
        raise ValueError('output already exists')
    worker = Path(__file__).with_name('tasks-sw.template.js').read_bytes()
    if b'__VERSION__' not in worker or b'__ASSETS__' not in worker:
        raise ValueError('invalid local service worker template')
    with tarfile.open(fileobj=io.BytesIO(data), mode='r:gz') as archive:
        members = archive.getmembers()
        names = set()
        total = 0
        for member in members:
            name = PurePosixPath(member.name)
            if (not member.isfile() or name.is_absolute() or '..' in name.parts or
                    '\\' in member.name or name.as_posix() != member.name or
                    member.name in names or member.size > MAX_FILE or
                    not (member.name in ('LICENSE', 'dev/puter-localhost.mjs') or
                         member.name.startswith('src/tasks/'))):
                raise ValueError('unsafe or unexpected source member: ' + member.name)
            names.add(member.name)
            total += member.size
        if total > MAX_TOTAL:
            raise ValueError('source archive too large')
        required = {'LICENSE', 'dev/puter-localhost.mjs', 'src/tasks/index.html', 'src/tasks/package-lock.json', 'src/tasks/sw.template.js'}
        if not required.issubset(names):
            raise ValueError('source archive missing required files')
        index = archive.extractfile('src/tasks/index.html').read()
        if index.count(SDK_TAG) != 1:
            raise ValueError('expected exactly one upstream SDK tag')
        for patched, (old, _new) in UI_PATCHES.items():
            if patched not in names or archive.extractfile(patched).read().count(old) != 1:
                raise ValueError('UI patch target changed in ' + patched)
        output.mkdir(parents=True)
        for member in members:
            destination = output.joinpath(*PurePosixPath(member.name).parts)
            destination.parent.mkdir(parents=True, exist_ok=True)
            content = archive.extractfile(member).read()
            if member.name == 'src/tasks/index.html':
                content = content.replace(SDK_TAG, LOCAL_TAG)
            if member.name == 'src/tasks/sw.template.js':
                content = worker
            if member.name in UI_PATCHES:
                old, new = UI_PATCHES[member.name]
                content = content.replace(old, new)
            destination.write_bytes(content)
        target = output / 'src/tasks/public/_homestead/puter-bridge.js'
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_bytes(bridge)


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('archive', type=Path)
    parser.add_argument('bridge', type=Path)
    parser.add_argument('output', type=Path)
    args = parser.parse_args()
    prepare(args.archive, args.bridge, args.output)


if __name__ == '__main__':
    main()
