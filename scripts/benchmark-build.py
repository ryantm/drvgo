#!/usr/bin/env python3
"""Compare recompiling every package in two exports, with tools and inputs warm."""

import argparse
import json
from pathlib import Path
import statistics
import subprocess
import tempfile
import time


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--before', type=Path, required=True)
    parser.add_argument('--after', type=Path, required=True)
    parser.add_argument('--runs', type=int, default=5)
    parser.add_argument('--out', type=Path)
    args = parser.parse_args()
    if args.runs < 1:
        parser.error('--runs must be positive')
    out = (args.out or Path(tempfile.mkdtemp(prefix='drvgo-rebuild-'))).resolve()
    out.mkdir(parents=True, exist_ok=True)
    if any(out.iterdir()):
        parser.error('--out must be an empty directory')
    roots = {'before': args.before.resolve(), 'after': args.after.resolve()}
    flags = ['--no-out-link', '--max-jobs', '4', '--cores', '2']
    expressions = {label: 'builtins.attrValues (import (builtins.toPath '
                   + json.dumps(str(root)).replace('${', r'\${') + ') {}).units'
                   for label, root in roots.items()}
    report = {
        'scope': 'Rebuild every package with nix-build --check; warm tools, sources, and dependency outputs. No network transfer.',
        'nix': subprocess.check_output(['nix', '--version'], text=True).strip(),
        'flags': flags, 'roots': {k: str(v) for k, v in roots.items()}, 'records': [],
    }
    for label, expression in expressions.items():
        with (out / (label + '-prepare.log')).open('w') as log:
            subprocess.run(['nix-build', *flags, '--expr', expression], stdout=log, stderr=log, check=True)
    for trial in range(args.runs + 1):
        for label in (['before', 'after'] if trial % 2 == 0 else ['after', 'before']):
            with (out / f'{label}-{trial}.log').open('w') as log:
                start = time.monotonic()
                subprocess.run(['nix-build', *flags, '--check', '--expr', expressions[label]],
                               stdout=log, stderr=log, check=True)
                elapsed = time.monotonic() - start
            report['records'].append(dict(label=label, trial=trial, warmup=trial == 0, seconds=elapsed))
            (out / 'results.json').write_text(json.dumps(report, indent=2) + '\n')
            print(f'{label} {trial}: {elapsed:.3f}s', flush=True)
    report['medians'] = {label: statistics.median(r['seconds'] for r in report['records']
                                                if r['label'] == label and not r['warmup']) for label in roots}
    (out / 'results.json').write_text(json.dumps(report, indent=2) + '\n')
    print(json.dumps(report['medians'], indent=2))


if __name__ == '__main__':
    main()
