#!/usr/bin/env python3
"""Stress short native builders through Nix to catch premature log-pipe closure."""

import argparse
import json
from pathlib import Path
import subprocess
import tempfile
import uuid


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--count', type=int, default=200)
    parser.add_argument('--out', type=Path)
    args = parser.parse_args()
    if args.count < 1:
        parser.error('--count must be positive')
    out = (args.out or Path(tempfile.mkdtemp(prefix='drvgo-forwarding-'))).resolve()
    out.mkdir(parents=True, exist_ok=True)
    if any(out.iterdir()):
        parser.error('--out must be empty')
    repo = Path(__file__).resolve().parent.parent
    nonce = uuid.uuid4().hex[:12]
    expression = '''
      let tc = import ./internal/export/templates/toolchain.nix;
          drvgo = import ./nix/package.nix { pkgs = tc.pkgs; };
          target = tc.pkgs.writeText "drvgo-forwarding-target" "forwarded data\\n";
      in builtins.genList (i: builtins.derivation {
        name = "drvgo-forwarding-NONCE-${toString i}";
        system = tc.pkgs.stdenv.hostPlatform.system;
        builder = "${drvgo}/bin/drvgo";
        args = [ "internal-symlink" target (builtins.placeholder "out") ];
        preferLocalBuild = true;
        allowSubstitutes = false;
      }) COUNT
    '''.replace('NONCE', nonce).replace('COUNT', str(args.count))
    command = ['nix-build', '--no-out-link', '--expr', expression,
               '--option', 'allow-import-from-derivation', 'false',
               '--option', 'sandbox', 'true', '--option', 'sandbox-fallback', 'false',
               '--option', 'always-allow-substitutes', 'false', '--max-jobs', '4', '--cores', '1']
    with (out / 'build.log').open('w') as log:
        outputs = subprocess.check_output(command, cwd=repo, stderr=log, text=True).splitlines()
    assert len(outputs) == len(set(outputs)) == args.count, outputs
    targets = set()
    for output in outputs:
        path = Path(output)
        assert path.is_symlink(), output
        assert path.read_bytes() == b'forwarded data\n', output
        targets.add(str(path.resolve()))
    assert len(targets) == 1, targets
    report = {'count': args.count, 'nonce': nonce, 'outputs': outputs,
              'target': targets.pop(), 'sandboxed_short_builders': 'passed'}
    (out / 'results.json').write_text(json.dumps(report, indent=2) + '\n')
    print(f'{args.count} sandboxed native forwarding builders PASS', flush=True)


if __name__ == '__main__':
    main()
