#!/usr/bin/env python3
"""Measure actual planner inputs, including recipe metadata, in a local binary cache."""

import argparse
import json
from pathlib import Path
import subprocess
import tempfile


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--before', required=True, help='baseline planner .drv path')
    parser.add_argument('--after', required=True, help='modified planner .drv path')
    parser.add_argument('--out', type=Path)
    args = parser.parse_args()
    out = (args.out or Path(tempfile.mkdtemp(prefix='drvgo-planner-closure-'))).resolve()
    out.mkdir(parents=True, exist_ok=True)
    if any(out.iterdir()):
        parser.error('--out must be an empty directory')
    report = {
        'scope': 'Planner input closures, including raw derivation metadata; local zstd level 3 cache; no network timing',
        'nix': subprocess.check_output(['nix', '--version'], text=True).strip(),
        'planners': {},
    }
    for label, path in [('before', args.before), ('after', args.after)]:
        data = json.loads(subprocess.check_output(['nix', 'derivation', 'show', path]))
        derivations = data.get('derivations', data)
        if len(derivations) != 1:
            raise RuntimeError(f'Expected one planner derivation: {path}')
        drv = next(iter(derivations.values()))
        # inputSrcs may contain .drv files. Copy those recipes without realizing
        # their outputs: those tools are only needed by the generated graph.
        if 'inputs' in drv:
            store = Path(path).parent
            sources = [str(store / p) for p in drv['inputs']['srcs']]
            inputs = drv['inputs']['drvs']
            if any(v.get('dynamicOutputs') for v in inputs.values()):
                raise RuntimeError('Planner itself has dynamic inputs; realize those separately')
            targets = [str(store / d) + '!' + ','.join(v['outputs']) for d, v in inputs.items()]
        else:
            sources = drv['inputSrcs']
            targets = [d + '!' + ','.join(outputs) for d, outputs in drv['inputDrvs'].items()]
        with (out / f'{label}-inputs.log').open('w') as log:
            realized = subprocess.check_output(['nix-store', '--realise', *targets], stderr=log, text=True).splitlines()
        roots = sorted(set(sources + realized))
        report['planners'][label] = {'derivation': path, 'roots': roots}
    cache = out / 'cache'
    roots = sorted({r for p in report['planners'].values() for r in p['roots']})
    with (out / 'copy.log').open('w') as log:
        subprocess.run(['nix', 'copy', '--to', cache.as_uri() + '?compression=zstd&compression-level=3', *roots],
                       stdout=log, stderr=log, check=True)
    sizes = {}
    for file in cache.glob('*.narinfo'):
        fields = dict(line.split(': ', 1) for line in file.read_text().splitlines() if ': ' in line)
        sizes[fields['StorePath']] = {'nar': int(fields['NarSize']), 'compressed': int(fields['FileSize'])}
    for planner in report['planners'].values():
        closure = set(subprocess.check_output(['nix-store', '--query', '--requisites', *planner['roots']], text=True).splitlines())
        planner.update(paths=len(closure), nar_bytes=sum(sizes[p]['nar'] for p in closure),
                       zstd_bytes=sum(sizes[p]['compressed'] for p in closure))
    (out / 'results.json').write_text(json.dumps(report, indent=2) + '\n')
    print(json.dumps(report, indent=2))


if __name__ == '__main__':
    main()
