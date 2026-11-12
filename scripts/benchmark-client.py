#!/usr/bin/env python3
"""Compare complete Nix and classic iets CI builds with the same daemon settings."""
import argparse
import json
import os
from pathlib import Path
import statistics
import subprocess
import tempfile
import time
import uuid


def nix_string(value):
    return json.dumps(str(value)).replace('${', r'\${')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--iets', type=Path, required=True, help='Nix-built bin/iets executable')
    parser.add_argument('--source', type=Path, required=True)
    parser.add_argument('--package', default='.')
    parser.add_argument('--vendor-hash')
    parser.add_argument('--module-cache', type=Path)
    parser.add_argument('--nested-iets', action='store_true', help='use the same iets package inside every planner')
    parser.add_argument('--runs', type=int, default=5)
    parser.add_argument('--nix-option', nargs=2, action='append', default=[], metavar=('NAME', 'VALUE'))
    parser.add_argument('--out', type=Path)
    args = parser.parse_args()
    if args.runs < 1:
        parser.error('--runs must be positive')
    binary = args.iets.resolve()
    iets = binary.parent.parent
    if iets.parent != Path('/nix/store'):
        parser.error('--iets must be a Nix store output bin/iets')
    out = (args.out or Path(tempfile.mkdtemp(prefix='drvgo-client-benchmark-'))).resolve()
    out.mkdir(parents=True, exist_ok=True)
    if any(out.iterdir()):
        parser.error('--out must be empty')
    repo = Path(__file__).resolve().parent.parent
    # Classic iets does not implement --extra-experimental-features. Use the
    # process-local configuration supported by both clients instead.
    config = os.environ.get('NIX_CONFIG', '') + '\nextra-experimental-features = nix-command ca-derivations dynamic-derivations recursive-nix\n'
    env = dict(os.environ, NIX_CONFIG=config, NIX_REMOTE='daemon',
               IETS_NIX_FALLBACK='never', IETS_CACHE='false', IETS_PROGRESS='false')
    settings = [('allow-import-from-derivation', 'false'), ('sandbox', 'true'),
                ('sandbox-fallback', 'false'), ('max-jobs', '4'), ('cores', '2'), *args.nix_option]
    flags = [part for name, value in settings for part in ['--option', name, value]]
    clients = {
        'nix': (['nix-build', '--no-out-link', '--store', 'daemon'], {}),
        'iets-1': ([str(binary), 'nix-build', '--no-out-link', '--store', 'daemon'], {'IETS_WORKERS': '1'}),
        'iets-4': ([str(binary), 'nix-build', '--no-out-link', '--store', 'daemon'], {'IETS_WORKERS': '4'}),
    }
    options = ''
    if args.vendor_hash:
        options += f'vendorHash = {nix_string(args.vendor_hash)};'
    if args.module_cache:
        options += f'moduleCache = builtins.storePath {nix_string(args.module_cache)};'
    if args.nested_iets:
        options += f'iets = builtins.storePath {nix_string(iets)};'
    nonce = uuid.uuid4().hex[:12]
    report = {
        'scope': 'Complete frontend evaluation and realization; fresh planner per trial, warm package outputs; classic iets forwards the same daemon settings as Nix; tracing cache, IR cache and fallback disabled',
        'source': str(args.source.resolve()), 'package': args.package, 'vendor_hash': args.vendor_hash,
        'module_cache': str(args.module_cache) if args.module_cache else None,
        'iets': str(binary), 'nested_iets': args.nested_iets, 'settings': settings, 'nonce': nonce,
        'nix_version': subprocess.check_output(['nix', '--version'], text=True).strip(),
        'iets_version': subprocess.check_output([str(binary), 'version'], text=True).strip(),
        'records': [],
    }
    expected = None
    for trial in range(args.runs + 1):
        names = list(clients) if trial % 2 == 0 else list(reversed(clients))
        for client in names:
            name = f'drvgo-client-{nonce}-{client}-{trial}'
            expr = (f'(import (builtins.toPath {nix_string(repo / "nix/dynamic.nix")}) {{}}) {{ name = {nix_string(name)}; '
                    f'src = builtins.path {{ path = {nix_string(args.source.resolve())}; name = "source"; }}; '
                    f'package = {nix_string(args.package)}; {options} }}')
            command, extra_env = clients[client]
            command = [*command, *flags, '-E', expr]
            with (out/f'{client}-{trial}.log').open('w') as log:
                start = time.monotonic()
                output = subprocess.check_output(command, cwd=repo, env=dict(env, **extra_env), stderr=log, text=True).strip()
                elapsed = time.monotonic() - start
            resolved = str(Path(output).resolve())
            if expected is None:
                expected = resolved
            if resolved != expected:
                raise RuntimeError(f'{client}: package output changed: {resolved} != {expected}')
            report['records'].append({'client': client, 'trial': trial, 'warmup': trial == 0,
                                      'seconds': elapsed, 'result': output, 'command_output': resolved,
                                      'command': command})
            (out/'results.json').write_text(json.dumps(report, indent=2)+'\n')
            print(f'{client} {trial}: {elapsed:.3f}s', flush=True)
    report['medians'] = {client: statistics.median(r['seconds'] for r in report['records']
                                                 if r['client'] == client and not r['warmup']) for client in clients}
    (out/'results.json').write_text(json.dumps(report, indent=2)+'\n')
    print(json.dumps(report['medians'], indent=2))


if __name__ == '__main__':
    main()
