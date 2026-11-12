#!/usr/bin/env python3
"""Compare Nix and iets evaluation of already-exported graphs, without builds."""

import argparse
import json
import os
from pathlib import Path
import statistics
import subprocess
import tempfile
import time


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--before', type=Path, required=True)
    parser.add_argument('--after', type=Path, required=True)
    parser.add_argument('--iets', type=Path, required=True)
    parser.add_argument('--after-export-data', action='store_true')
    parser.add_argument('--after-early-cutoff', action='store_true')
    parser.add_argument('--runs', type=int, default=7)
    parser.add_argument('--out', type=Path)
    args = parser.parse_args()
    if args.runs < 1:
        parser.error('--runs must be positive')
    if args.after_early_cutoff and not args.after_export_data:
        parser.error('--after-early-cutoff requires --after-export-data')
    out = (args.out or Path(tempfile.mkdtemp(prefix='drvgo-evaluation-'))).resolve()
    out.mkdir(parents=True, exist_ok=True)
    if any(out.iterdir()):
        parser.error('--out must be an empty directory')
    env = dict(os.environ, IETS_NIX_FALLBACK='never', IETS_PROGRESS='false', NIX_REMOTE='daemon')
    commands = {
        'nix': ['nix-instantiate', '--eval', '--strict', '--raw', '--read-write-mode'],
        'iets-1': [str(args.iets.resolve()), 'eval', '--impure', '--raw', '--no-tracing-cache', '--workers', '1'],
        'iets-4': [str(args.iets.resolve()), 'eval', '--impure', '--raw', '--no-tracing-cache', '--workers', '4'],
    }
    roots = {'before': args.before.resolve(), 'after': args.after.resolve()}
    report = {'scope': 'Warm graph evaluation; no builds; iets tracing cache and Nix fallback disabled',
              'after_export_data': args.after_export_data, 'after_early_cutoff': args.after_early_cutoff,
              'roots': {k: str(v) for k, v in roots.items()}, 'records': []}
    expected = {}
    cases = [(label, client) for label in roots for client in commands]
    for trial in range(args.runs + 1):
        for label, client in (cases if trial % 2 == 0 else list(reversed(cases))):
            options = ''
            if label == 'after' and args.after_export_data:
                options += 'exportData = true;'
            if label == 'after' and args.after_early_cutoff:
                options += 'earlyCutoff = true;'
            expression = '(import (builtins.toPath ' + json.dumps(str(roots[label])).replace('${', r'\${') + ') {' + options + '}).default.drvPath'
            command = [*commands[client], '--extra-experimental-features', 'ca-derivations',
                       '--option', 'allow-import-from-derivation', 'false', '-E', expression]
            with (out / f'{label}-{client}-{trial}.log').open('w') as log:
                start = time.monotonic()
                value = subprocess.check_output(command, stderr=log, env=env, text=True).strip()
                elapsed = time.monotonic() - start
            if label in expected and value != expected[label]:
                raise RuntimeError(f'{label}/{client}: derivation changed: {value} != {expected[label]}')
            expected[label] = value
            report['records'].append(dict(label=label, client=client, trial=trial, warmup=trial == 0,
                                          seconds=elapsed, derivation=value))
            (out / 'results.json').write_text(json.dumps(report, indent=2) + '\n')
    report['medians'] = {label: {client: statistics.median(r['seconds'] for r in report['records']
                              if r['label'] == label and r['client'] == client and not r['warmup'])
                              for client in commands} for label in roots}
    (out / 'results.json').write_text(json.dumps(report, indent=2) + '\n')
    print(json.dumps(report['medians'], indent=2))


if __name__ == '__main__':
    main()
