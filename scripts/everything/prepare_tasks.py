#!/usr/bin/env python3
"""Prepare the pinned Tasks source (S367). Thin wrapper over prepare_app.py,
kept so the S367 tests and gates keep their entry point. New code should use
prepare_app.py <app> ..."""
import argparse
import importlib.util
from pathlib import Path

_spec = importlib.util.spec_from_file_location('prepare_app', Path(__file__).with_name('prepare_app.py'))
_generic = importlib.util.module_from_spec(_spec)
_spec.loader.exec_module(_generic)

_DEF = _generic.load_definition('tasks')
EXPECTED_SHA256 = _DEF['sha256']


def prepare(archive_path: Path, bridge_path: Path, output: Path) -> None:
    _generic.prepare_app(_DEF, archive_path, bridge_path, output, expected_sha256=EXPECTED_SHA256)


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('archive', type=Path)
    parser.add_argument('bridge', type=Path)
    parser.add_argument('output', type=Path)
    args = parser.parse_args()
    prepare(args.archive, args.bridge, args.output)


if __name__ == '__main__':
    main()
