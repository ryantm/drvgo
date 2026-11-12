#!/usr/bin/env python3
"""Check reuse of a declared previous planner graph without reusing stale discovery."""
import argparse
import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import uuid


def nix_string(value):
    return json.dumps(str(value)).replace('${', r'\${')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--flake-toolchain', default='toolchain-go1_26')
    parser.add_argument('--iets', type=Path, help='also use this Nix-built evaluator inside planners')
    parser.add_argument('--out', type=Path)
    args = parser.parse_args()
    repo = Path(__file__).resolve().parent.parent
    out = (args.out or Path(tempfile.mkdtemp(prefix='drvgo-previous-plan-'))).resolve()
    out.mkdir(parents=True, exist_ok=True)
    if any(out.iterdir()):
        parser.error('--out must be empty')
    source = out / 'source'
    shutil.copytree(repo/'tests/fixture', source)
    iets = args.iets.resolve() if args.iets else None
    if iets and iets.parent.parent.parent != Path('/nix/store'):
        parser.error('--iets must be a Nix store output bin/iets')
    nonce = uuid.uuid4().hex[:12]
    env = dict(os.environ, NIX_REMOTE='daemon', IETS_CACHE='false', IETS_NIX_FALLBACK='never', IETS_PROGRESS='false')
    env['NIX_CONFIG'] = env.get('NIX_CONFIG', '') + '\nextra-experimental-features = nix-command ca-derivations dynamic-derivations recursive-nix\n'
    flags = ['--option', 'sandbox', 'true', '--option', 'sandbox-fallback', 'false',
             '--option', 'allow-import-from-derivation', 'false', '--option', 'substituters', '',
             '--option', 'always-allow-substitutes', 'false', '--max-jobs', '4', '--cores', '2']
    report = {'toolchain': args.flake_toolchain, 'iets': str(iets) if iets else None, 'checks': []}

    def run(command, label, error=None):
        command = [str(x) for x in command]
        result = subprocess.run(command, cwd=repo, env=env, capture_output=True, text=True)
        (out/f'{label}.log').write_text(result.stdout + result.stderr)
        if error is not None:
            assert result.returncode != 0 and error in result.stderr, (label, result.returncode, result.stderr)
        elif result.returncode:
            raise RuntimeError(f'{label}: {result.stderr}')
        return result.stdout.strip()

    def expression(label, previous=None, src=source, extra=''):
        options = f'previousPlan = builtins.storePath {nix_string(previous)};' if previous else ''
        if iets:
            options += f'iets = builtins.storePath {nix_string(iets.parent.parent)};'
        return (f'let f = builtins.getFlake {nix_string(repo)}; system = builtins.currentSystem; '
                f'in f.lib.mkGoPackage {{ inherit system; go = f.packages.${{system}}.{args.flake_toolchain}.go; '
                f'src = builtins.path {{ path = {nix_string(src)}; name = "source"; }}; '
                f'name = "drvgo-previous-{nonce}-{label}"; {options} {extra} }}')

    def build(expr, label, error=None):
        return run(['nix-build', *flags, '--no-out-link', '--expr', expr], label, error)

    def plan(label, previous=None, src=source, extra=''):
        result = build(f'({expression(label, previous, src, extra)}).planner', label)
        assert Path(result).is_file() and result.endswith('.drv'), result
        return result

    seed = plan('seed')
    expected = 'hello embedded world\nstable\nnested asset\n01 02 ff\nhidden asset'

    def compare(label, expected, previous=seed, extra=''):
        plain = expression(label+'-plain', extra=extra)
        cached = expression(label+'-cached', previous, extra=extra)
        outputs = [str(Path(build(e, label+'-'+str(i))).resolve()) for i, e in enumerate([plain, cached])]
        assert outputs[0] == outputs[1], (label, outputs)
        assert run([Path(outputs[0])/'bin/libnix-fixture'], label+'-run') == expected
        # The public helper must carry the prior graph as a real planner input.
        drv = run(['nix-instantiate', *flags, '--expr', f'({cached}).planner'], label+'-planner')
        refs = run(['nix-store', '--query', '--references', drv], label+'-inputs').splitlines()
        assert previous in refs, (previous, refs)
        if iets:
            nix_drv = run(['nix-instantiate', *flags, '--eval', '--read-write-mode', '--raw',
                           '--expr', f'({cached}).drvPath'], label+'-nix-eval')
            for workers in [1, 4]:
                actual = run([iets, 'eval', '--impure', '--raw', '--workers', workers,
                              '--option', 'allow-import-from-derivation', 'false',
                              '-E', f'({cached}).drvPath'], label+f'-iets-{workers}')
                assert actual == nix_drv, (label, workers, actual, nix_drv)
        report['checks'].append({'case': label, 'output': outputs[0]})
        print(label, 'fresh/cached/output/declared-input PASS', flush=True)
        return outputs[0]

    original = compare('unchanged', expected)
    # Changing linker flags must rediscover compiler recipes even when the
    # previous graph is visible. A stripped archive cannot seed a debug build.
    stripped = compare('strip-after-debug', expected, extra='ldflags = "-s -w";')
    assert stripped != original
    stripped_plan = plan('stripped-plan', extra='ldflags = "-s -w";')
    assert compare('debug-after-strip', expected, stripped_plan) == original
    for cutoff in [False, True]:
        mode = 'cutoff' if cutoff else 'interfaces'
        extra = f'exportData = true; earlyCutoff = {str(cutoff).lower()};'
        mode_plan = plan(mode+'-plan', extra=extra)
        compare(mode, expected, mode_plan, extra)
    (source/'message/assets/message.txt').write_text('previous plan changed embed\n')
    expected = expected.replace('hello embedded world', 'previous plan changed embed')
    changed = compare('embed-edit', expected)
    assert changed != original
    (source/'fresh').mkdir()
    (source/'fresh/fresh.go').write_text('package fresh\nfunc Value() string { return "fresh dependency" }\n')
    main_file = source/'main.go'
    main_file.write_text(main_file.read_text().replace('import (', 'import (\n "example.com/libnix-fixture/fresh"', 1)
                         .replace('func main() {', 'func main() {\n defer fmt.Println(fresh.Value())', 1))
    newer = compare('new-dependency', expected+'\nfresh dependency')
    assert newer != changed
    updated = plan('updated-plan', seed)
    closure = run(['nix-store', '--query', '--requisites', updated], 'updated-closure').splitlines()
    assert seed not in closure, 'new plans must not accumulate previous plan roots'
    report['previous_plan_not_retained'] = True

    main_file.write_text(main_file.read_text()+'\nvar broken int = "DRVGO_PREVIOUS_PLAN_INVALID"\n')
    for label, previous in [('plain', None), ('cached', seed)]:
        build(expression('invalid-'+label, previous), 'invalid-'+label, 'DRVGO_PREVIOUS_PLAN_INVALID')
    report['invalid_after_previous_plan'] = 'passed'

    optional = out/'optional'
    optional.mkdir()
    (optional/'go.mod').write_text('module example.com/previous-tests\ngo 1.25\n')
    sample = optional/'sample.go'
    sample.write_text('package sample\nconst Value = 42\n')
    extra = 'test = true; allowEmptyTests = true; cgoEnabled = false;'
    empty_plan = plan('empty-plan', src=optional, extra=extra)
    empty = build(expression('empty-cached', empty_plan, optional, extra), 'empty-cached')
    assert json.loads((Path(empty)/'nix-support/no-tests').read_text()) == {'version': 1, 'status': 'no-tests'}
    sample.write_text('package sample\nvar Value int = "DRVGO_PREVIOUS_EMPTY_INVALID"\n')
    build(expression('empty-invalid', empty_plan, optional, extra), 'empty-invalid', 'DRVGO_PREVIOUS_EMPTY_INVALID')
    report['empty_test_validation_after_previous_plan'] = 'passed'
    (out/'results.json').write_text(json.dumps(report, indent=2)+'\n')
    print('obsolete-plan/nonaccumulation/invalid-source/empty-test-validation PASS', flush=True)


if __name__ == '__main__':
    main()
