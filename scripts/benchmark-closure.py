#!/usr/bin/env python3
"""Measure toolchain closures and their zstd binary-cache payloads (no network)."""

import argparse
import json
from pathlib import Path
import subprocess
import tempfile


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--before', type=Path, required=True, help='baseline toolchain store output')
    parser.add_argument('--after', type=Path, required=True, help='new toolchain store output')
    parser.add_argument('--out', type=Path)
    args = parser.parse_args()
    out = (args.out or Path(tempfile.mkdtemp(prefix='drvgo-closure-'))).resolve()
    out.mkdir(parents=True, exist_ok=True)
    if any(out.iterdir()):
        parser.error('--out must be an empty directory')
    roots = {}
    for label, root in [('before', args.before.resolve()), ('after', args.after.resolve())]:
        roots[label] = {'planning': str(root)}
        for role in ['compile', 'link', 'native', 'internal-link']:
            file = root / 'nix-support' / f'drvgo-{role}-toolchain'
            fallback = roots[label]['native'] if role == 'internal-link' else str(root)
            roots[label][role] = file.read_text().strip() if file.exists() else fallback
    paths = sorted({p for roles in roots.values() for p in roles.values()})
    cache = out / 'cache'
    with (out / 'copy.log').open('w') as log:
        subprocess.run(['nix', 'copy', '--to', cache.as_uri() + '?compression=zstd&compression-level=3', *paths],
                       stdout=log, stderr=log, check=True)
    sizes = {}
    for file in cache.glob('*.narinfo'):
        fields = dict(line.split(': ', 1) for line in file.read_text().splitlines() if ': ' in line)
        sizes[fields['StorePath']] = {'nar': int(fields['NarSize']), 'compressed': int(fields['FileSize'])}
    report = {'scope': 'Sum of NAR and zstd level 3 file sizes for complete closures; local binary cache, no network timing',
              'nix': subprocess.check_output(['nix', '--version'], text=True).strip(), 'toolchains': {}}
    for label, roles in roots.items():
        report['toolchains'][label] = {}
        for role, path in roles.items():
            closure = subprocess.check_output(['nix-store', '--query', '--requisites', path], text=True).splitlines()
            report['toolchains'][label][role] = {
                'path': path, 'paths': len(closure), 'nar_bytes': sum(sizes[p]['nar'] for p in closure),
                'zstd_bytes': sum(sizes[p]['compressed'] for p in closure),
            }
    (out / 'results.json').write_text(json.dumps(report, indent=2) + '\n')
    print(json.dumps(report, indent=2))


if __name__ == '__main__':
    main()
