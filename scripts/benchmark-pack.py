#!/usr/bin/env python3
"""Compare replaying an archive append with warm worker tools and input objects."""

import argparse
import hashlib
import json
import os
from pathlib import Path
import statistics
import subprocess
import tempfile
import time


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--before', type=Path, required=True, help='baseline toolchain output')
    parser.add_argument('--after', type=Path, required=True, help='modified toolchain output')
    parser.add_argument('--runs', type=int, default=5)
    parser.add_argument('--iterations', type=int, default=100)
    parser.add_argument('--out', type=Path)
    args = parser.parse_args()
    if args.runs < 1 or args.iterations < 1:
        parser.error('--runs and --iterations must be positive')
    out = (args.out or Path(tempfile.mkdtemp(prefix='drvgo-pack-bench-'))).resolve()
    out.mkdir(parents=True, exist_ok=True)
    if any(out.iterdir()):
        parser.error('--out must be empty')
    toolchains = {'before': args.before.resolve(), 'after': args.after.resolve()}
    go_env = dict(os.environ, GOENV='off', GOFLAGS='', GOTOOLCHAIN='local', CGO_ENABLED='0', GOPROXY='off')
    versions = {label: subprocess.check_output([tc/'bin/go', 'env', 'GOVERSION'], env=go_env, text=True).strip()
                for label, tc in toolchains.items()}
    if versions['before'] != versions['after']:
        parser.error('toolchains must select the same Go version')
    go = toolchains['before']/'bin/go'
    sdk, arch = subprocess.check_output([go, 'env', 'GOROOT', 'GOARCH'], env=go_env, text=True).splitlines()
    (out/'sample.go').write_text('package sample\nfunc Answer() uint64\n')
    instructions = {
        'amd64': 'MOVQ $42, ret+0(FP)\n RET',
        'arm64': 'MOVD $42, R0\n MOVD R0, ret+0(FP)\n RET',
    }
    if arch not in instructions:
        parser.error('benchmark supports amd64 and arm64')
    (out/'sample.s').write_text('#include "textflag.h"\nTEXT ·Answer(SB), NOSPLIT, $0-8\n ' + instructions[arch] + '\n')
    subprocess.run([go, 'tool', 'compile', '-pack', '-p', 'sample', '-o', out/'base.a', out/'sample.go'], env=go_env, check=True)
    subprocess.run([go, 'tool', 'asm', '-p', 'sample', '-I', Path(sdk)/'pkg/include', '-o', out/'sample.o', out/'sample.s'], env=go_env, check=True)
    quote = lambda value: "'" + str(value).replace("'", "'\\''") + "'"
    script = out/'script'
    script.write_text(f"cp {quote(out/'base.a')} @SELF@/_pkg_.a\nlibnix-pack r @SELF@/_pkg_.a {quote(out/'sample.o')}\n")
    report = {
        'scope': 'One assembly-object append per worker process; warm tools and inputs; includes worker startup and archive publication; no compilation, Nix scheduling, or network timing',
        'go': versions['before'], 'arch': arch, 'iterations': args.iterations,
        'toolchains': {k: str(v) for k, v in toolchains.items()}, 'records': [],
    }
    expected = None
    for trial in range(args.runs + 1):
        for label in ['before', 'after'] if trial % 2 == 0 else ['after', 'before']:
            tc = toolchains[label]
            work = out/f'{label}-{trial}'
            work.mkdir()
            env = dict(go_env, PATH=str(tc/'bin'), TMPDIR=str(work), out=str(work/'out'),
                       scriptPath=str(script), NIX_BUILD_CORES='1', isBinary='0', emptyTests='0',
                       GOROOT=str(tc/'share/go'), toolchainInput=str(tc))
            with (out/f'{label}-{trial}.log').open('w') as log:
                start = time.monotonic()
                for _ in range(args.iterations):
                    subprocess.run([tc/'bin/drvgo-builder'], env=env, stdout=log, stderr=log, check=True)
                elapsed = time.monotonic() - start
            digest = hashlib.sha256((work/'out/_pkg_.a').read_bytes()).hexdigest()
            if expected is None:
                expected = digest
            assert digest == expected, (label, digest, expected)
            report['records'].append(dict(label=label, trial=trial, warmup=trial == 0,
                                          seconds=elapsed, seconds_per_archive=elapsed/args.iterations, sha256=digest))
            print(label, trial, f'{elapsed/args.iterations*1000:.3f} ms/archive', flush=True)
    report['medians'] = {label: statistics.median(r['seconds_per_archive'] for r in report['records']
                                                 if r['label'] == label and not r['warmup']) for label in toolchains}
    (out/'results.json').write_text(json.dumps(report, indent=2)+'\n')
    print(json.dumps(report['medians'], indent=2))


if __name__ == '__main__':
    main()
