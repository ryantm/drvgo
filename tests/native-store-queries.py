#!/usr/bin/env python3
"""Check native queries against the restricted daemon without Nix commands in PATH."""
import argparse
import json
from pathlib import Path
import subprocess
import tempfile
import uuid


def nix_string(value):
    return json.dumps(str(value)).replace('${', r'\${')


CHECK = r'''
import json, os, shutil, subprocess
from pathlib import Path
assert shutil.which('nix') is None and shutil.which('nix-store') is None
root = Path(os.environ['TMPDIR'])
os.environ['HOME'] = str(root/'home')
(root/'home').mkdir()
source = root/'source'
source.mkdir()
(source/'go.mod').write_text('module example.com/store-query\ngo 1.25\n')
visible, hidden = os.environ['visible'], os.environ['hidden']
missing = '/nix/store/00000000000000000000000000000000-drvgo-query-missing'
(source/'main.go').write_text('package main\nvar Paths = []string{' + ','.join(json.dumps(p) for p in [visible, hidden, missing]) + '}\nfunc main() { println(Paths[0]) }\n')
graph = root/'graph'
command = [os.environ['cli'], 'export', '-C', str(source), '-toolchain', os.environ['toolchain'], '-module-proxy', 'off', '-cgo', '0', '-out', str(graph), '.']
subprocess.run(command, check=True)
data = json.loads((graph/'graph.json').read_text())
references = sorted({p for u in data['units'] for p in u.get('storeReferences', [])})
assert visible in references and hidden not in references and missing not in references, references
resolved = (graph/'resolved-toolchain.nix').read_text()
assert resolved.startswith('throw ') and 'import ./toolchain.nix' not in resolved, resolved
failed = root/'failed-graph'
command[command.index('-out')+1] = str(failed)
env = dict(os.environ, NIX_REMOTE='unix://'+str(root/'absent-socket'))
result = subprocess.run(command, env=env, text=True, capture_output=True)
assert result.returncode != 0 and 'query runtime references' in result.stderr, result.stderr
assert not failed.exists(), 'failed query published a graph'
Path(os.environ['out']).write_text(json.dumps({'visible': visible, 'hidden': hidden, 'missing': missing, 'references': references, 'censored_deriver': 'explicit toolchain required', 'broken_socket': 'failed without publishing graph', 'nix_commands': 'absent'}, indent=2)+'\n')
'''


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--out', type=Path)
    args = parser.parse_args()
    repo = Path(__file__).resolve().parent.parent
    out = (args.out or Path(tempfile.mkdtemp(prefix='drvgo-native-queries-'))).resolve()
    out.mkdir(parents=True, exist_ok=True)
    if any(out.iterdir()):
        parser.error('--out must be empty')
    base = f'let f = builtins.getFlake {nix_string(repo)}; tc = f.packages.${{builtins.currentSystem}}.toolchain-go1_26; pkgs = tc.pkgs; in '
    flags = ['--extra-experimental-features', 'ca-derivations dynamic-derivations recursive-nix',
             '--option', 'allow-import-from-derivation', 'false', '--option', 'sandbox', 'true',
             '--option', 'sandbox-fallback', 'false']
    def run(expr, label):
        with (out/(label+'.log')).open('w') as log:
            return subprocess.check_output(['nix-build', *flags, '--no-out-link', '--expr', expr], stderr=log, text=True).strip()
    # Realize this first, then supply its name without Nix string context. It is
    # valid in the host store but deliberately absent from the build's inputs.
    hidden = run(base + 'pkgs.writeText "drvgo-query-hidden" "unrelated store data"', 'hidden')
    expr = base + f'''let
      drvgo = f.packages.${{builtins.currentSystem}}.drvgo;
      planning = import {repo}/nix/planning-toolchain.nix {{ inherit pkgs drvgo; go = tc.go; }};
    in builtins.derivation {{
      name = "drvgo-native-query-check-{uuid.uuid4().hex[:12]}";
      system = builtins.currentSystem;
      builder = "${{pkgs.python3}}/bin/python3";
      args = [ (pkgs.writeText "check-native-queries.py" {nix_string(CHECK)}) ];
      PATH = pkgs.lib.makeBinPath [ pkgs.coreutils ];
      cli = "${{drvgo}}/bin/drvgo";
      toolchain = planning;
      visible = pkgs.writeText "drvgo-query-visible" "declared runtime data";
      hidden = {nix_string(hidden)};
      requiredSystemFeatures = [ "recursive-nix" ];
      preferLocalBuild = true;
      allowSubstitutes = false;
    }}'''
    result = run(expr, 'restricted')
    data = json.loads(Path(result).read_text())
    data['result'] = result
    (out/'results.json').write_text(json.dumps(data, indent=2)+'\n')
    print('native queries/restricted visibility/no CLI/fatal query failure PASS', flush=True)


if __name__ == '__main__':
    main()
