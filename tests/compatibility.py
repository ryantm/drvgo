#!/usr/bin/env python3
"""Differential build and cache tests for one explicitly selected Go toolchain."""
import argparse
import gzip
import json
import os
import platform
from pathlib import Path
import re
import shutil
import subprocess
import tempfile

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument('--toolchain', type=Path, required=True)
parser.add_argument('--drvgo', type=Path, required=True)
parser.add_argument('--integration-test', type=Path, required=True)
parser.add_argument('--out', type=Path)
parser.add_argument('--record-traces', action='store_true')
parser.add_argument('--flake-toolchain', help='also test mkGoPackage with this flake toolchain attribute')
a = parser.parse_args()
repo = Path(__file__).resolve().parent.parent
out = (a.out or Path(tempfile.mkdtemp(prefix='drvgo-compat-'))).resolve()
out.mkdir(parents=True, exist_ok=True)
tc, cli = a.toolchain.resolve(), a.drvgo.resolve()
env = {k:v for k,v in os.environ.items() if not (k.startswith(('GO','CGO_','NIX_','DEVELOPER_DIR_','MACOSX_DEPLOYMENT_TARGET_')) or k in ('CC','CXX','PATH','SDKROOT','DEVELOPER_DIR','MACOSX_DEPLOYMENT_TARGET'))}
cc, cxx = ('clang', 'clang++') if platform.system() == 'Darwin' else ('gcc', 'g++')
env.update(PATH=str(tc/'bin')+':'+os.environ['PATH'], GOENV='off', GOFLAGS='', GOTOOLCHAIN='local', CGO_ENABLED='1',
           GOMAXPROCS='2', GOPROXY='off', CC=str(tc/'bin'/cc), CXX=str(tc/'bin'/cxx))

def run(cmd, cwd=None, go=False, cgo=None):
    command_env = dict(env, CGO_ENABLED=cgo) if go and cgo is not None else env if go else None
    p = subprocess.run([str(x) for x in cmd], cwd=cwd, env=command_env, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
    if p.returncode:
        raise RuntimeError(f'{cmd}: {p.returncode}\n{p.stdout.decode()}\n{p.stderr.decode()}')
    return p

def write(path, content):
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(content)

integration_pattern = '^(TestEmbeddedAssetsAndCacheIsolation|TestNestedCommandWithoutOriginalSource|TestCustomBuildID|TestEmptyTest.*|TestTestBinary.*)$'
integration_tests = run([a.integration_test.resolve(), '-test.list='+integration_pattern]).stdout.decode().splitlines()
required_tests = {'TestEmbeddedAssetsAndCacheIsolation', 'TestTestBinaryInternalExternalAndFailure',
                  'TestTestBinaryMainPackage', 'TestTestBinaryRaceAndCGO', 'TestTestBinaryRejectsPackageWithoutTests',
                  'TestNestedCommandWithoutOriginalSource', 'TestCustomBuildID',
                  'TestEmptyTestSelection', 'TestEmptyTestResult', 'TestEmptyTestsRequireTestMode'}
if not required_tests.issubset(integration_tests):
    raise RuntimeError('Integration binary lacks required tests; compile it with go test -c -tags integration ./internal/export')

version = run([tc/'bin/go','env','GOVERSION'], go=True).stdout.decode().strip()
report = {'go':version, 'platform':run([tc/'bin/go','env','GOOS','GOARCH'], go=True).stdout.decode().split(),
          'toolchain':str(tc), 'cases':[], 'nix':run(['nix','--version']).stdout.decode().strip()}

def case(name, source, expected, tags='', ldflags='', cgo=None, race=False):
    native = out/(name+'-native')
    flags = ['-trimpath','-buildvcs=false','-tags',tags,'-ldflags',ldflags]
    if race: flags.append('-race')
    run([tc/'bin/go','build',*flags,'-o',native,'.'], cwd=source, go=True, cgo=cgo)
    normal = run([native]).stdout.decode()
    assert normal == expected, (name,normal,expected)
    export = out/(name+'-export')
    modes = (['-cgo',cgo] if cgo is not None else []) + (['-race'] if race else [])
    run([cli,'export','-C',source,'-out',export,'-toolchain',tc,'-module-proxy','off','-tags',tags,'-ldflags',ldflags,*modes,'.'])
    graph = json.loads((export/'graph.json').read_text())
    assert [graph['goos'], graph['goarch']] == report['platform'], graph
    result = run(['nix-build',export,'-A','default','--no-out-link','--max-jobs','4','--cores','2']).stdout.decode().strip()
    binary = Path(result)/'bin'/graph['binaryName']
    if report['platform'] == ['darwin', 'arm64']:
        header = binary.read_bytes()[:8]
        assert header == b'\xcf\xfa\xed\xfe\x0c\x00\x00\x01', ('expected native arm64 Mach-O', header)
    actual = run([binary]).stdout.decode()
    assert actual == normal, (name,actual,normal)
    paths = json.loads(run(['nix-instantiate','--eval','--strict','--json','--expr',
        'builtins.mapAttrs (_: d: d.drvPath) (import '+str(export)+' {}).units']).stdout)
    report['cases'].append({'name':name,'output':actual,'units':len(graph['units']),'store_path':result,
        'package_derivations':{u['name']:paths[u['id']] for u in graph['units']}})
    (out/'results.json').write_text(json.dumps(report,indent=2)+'\n')
    print(version, name, 'PASS',len(graph['units']),'units',flush=True)
    if a.record_traces and name=='embed-cgo':
        trace = run([tc/'bin/go','build','-n','-a','-work','-p','1',*flags,'-o','/LIBNIX_OUTPUT','.'],cwd=source,go=True).stderr
        trace = trace.replace(str(source).encode(),b'/fixture')
        trace = re.sub(rb'^WORK=[^\n]*',b'WORK=/fixture-work',trace,flags=re.M)
        target = repo/'internal/plan/testdata'/(version+'.trace.gz')
        target.parent.mkdir(parents=True,exist_ok=True)
        target.write_bytes(gzip.compress(trace,mtime=0))

fixture = out/'fixture'
shutil.copytree(repo/'tests/fixture',fixture,ignore=shutil.ignore_patterns('.drvgo*'))
case('embed-cgo',fixture,'hello embedded world\nstable\nnested asset\n01 02 ff\nhidden asset\n')
assembly = out/'assembly'
write(assembly/'go.mod','module example.com/compat\ngo 1.23.0\n')
write(assembly/'main.go','package main\nimport("fmt";"example.com/compat/asm")\nvar release="dev"\nfunc main(){fmt.Printf("%d %s %s\\n",asm.Answer(),mode,release)}\n')
write(assembly/'default.go','//go:build !compat\n\npackage main\nconst mode="default"\n')
write(assembly/'tagged.go','//go:build compat\n\npackage main\nconst mode="tagged"\n')
write(assembly/'asm/answer.go','package asm\nfunc Answer() uint64\n')
write(assembly/'asm/answer_amd64.s','#include "textflag.h"\nTEXT ·Answer(SB), NOSPLIT, $0-8\n MOVQ $42, ret+0(FP)\n RET\n')
write(assembly/'asm/answer_arm64.s','#include "textflag.h"\nTEXT ·Answer(SB), NOSPLIT, $0-8\n MOVD $42, R0\n MOVD R0, ret+0(FP)\n RET\n')
case('assembly',assembly,'42 default dev\n')
case('tags-ldflags',assembly,'42 tagged matrix\n',tags='compat',ldflags='-X main.release=matrix')
case('external-link',assembly,'42 default dev\n',ldflags='-linkmode=external')
workspace=out/'workspace'
write(workspace/'go.work','go 1.23.0\nuse (\n ./app\n ./dep\n)\n')
write(workspace/'app/go.mod','module example.com/app\ngo 1.23.0\nrequire example.com/dep v0.0.0\n')
write(workspace/'app/main.go','package main\nimport("fmt";"example.com/dep")\nfunc main(){fmt.Println(dep.Value)}\n')
write(workspace/'dep/go.mod','module example.com/dep\ngo 1.23.0\n')
write(workspace/'dep/dep.go','package dep\nconst Value="workspace"\n')
case('workspace',workspace/'app','workspace\n')
vendor=out/'vendored'
write(vendor/'go.mod',(workspace/'app/go.mod').read_text())
write(vendor/'main.go',(workspace/'app/main.go').read_text())
write(vendor/'vendor/modules.txt','# example.com/dep v0.0.0\n## explicit; go 1.23.0\nexample.com/dep\n')
write(vendor/'vendor/example.com/dep/dep.go','package dep\nconst Value="vendored"\n')
case('vendored',vendor,'vendored\n')
modes = out/'build-modes'
write(modes/'go.mod','module example.com/modes.test\ngo 1.25\n')
write(modes/'main.go','package main\nimport "fmt"\nfunc main(){fmt.Println(Value())}\n')
write(modes/'native.go','package main\n/* static int answer(void) { return 42; } */\nimport "C"\nconst expected=42\nfunc Value() int { return int(C.answer()) }\n')
write(modes/'pure.go','//go:build !cgo\n\npackage main\nconst expected=7\nfunc Value() int { return 7 }\n')
case('cgo-disabled',modes,'7\n',cgo='0')
case('race-cgo',modes,'42\n',cgo='1',race=True)
cxx = out/'cxx'
write(cxx/'go.mod','module example.com/cxx\ngo 1.23.0\n')
write(cxx/'main.go','package main\n/* int answer(void); */\nimport "C"\nimport "fmt"\nfunc main(){fmt.Println(C.answer())}\n')
write(cxx/'answer.cc','#include <vector>\nextern "C" int answer(void) { std::vector<int> values{20, 22}; return values[0] + values[1]; }\n')
case('cxx',cxx,'42\n')
if report['platform'] == ['darwin', 'arm64']:
    framework = out/'framework'
    write(framework/'go.mod','module example.com/framework\ngo 1.23.0\n')
    write(framework/'main.go','package main\n/*\n#cgo LDFLAGS: -framework CoreFoundation\n#include <CoreFoundation/CoreFoundation.h>\nstatic int answer(void) { return (int)CFStringGetLength(CFSTR("hello")); }\n*/\nimport "C"\nimport "fmt"\nfunc main(){fmt.Println(C.answer())}\n')
    case('darwin-framework',framework,'5\n')
checkenv=dict(os.environ,DRVGO_TEST_TOOLCHAIN=str(tc))
p=subprocess.run([str(a.integration_test.resolve()),'-test.run='+integration_pattern,'-test.v'],cwd=repo/'internal/export',env=checkenv,stdout=subprocess.PIPE,stderr=subprocess.STDOUT)
(out/'integration.log').write_bytes(p.stdout)
if p.returncode: raise RuntimeError(p.stdout.decode())
if a.flake_toolchain:
    system = run(['nix','eval','--impure','--raw','--expr','builtins.currentSystem']).stdout.decode()
    result = run(['nix-build',repo/'tests/dynamic-compatibility.nix','--argstr','system',system,
        '--argstr','toolchainAttr',a.flake_toolchain,'--option','allow-import-from-derivation','false',
        '--max-jobs','4','--cores','2','-o',out/'dynamic-result']).stdout.decode().strip()
    executable = Path(result)/'bin/libnix-fixture'
    assert run([executable]).stdout.decode() == 'hello embedded world\nstable\nnested asset\n01 02 ff\nhidden asset\n'
    assert version in run([tc/'bin/go','version',executable],go=True).stdout.decode()
    assert Path(result).resolve() == Path(report['cases'][0]['store_path']), 'dynamic and standalone builds must share package outputs'
    report['dynamic_ifd_disabled']='passed'
    print(version,'dynamic/IFD-disabled PASS',flush=True)
    planner_cached = run(['nix-build',repo/'tests/dynamic-compatibility.nix','--argstr','system',system,
        '--argstr','toolchainAttr',a.flake_toolchain,'--arg','allowPlannerSubstitutes','true',
        '--option','allow-import-from-derivation','false','--max-jobs','4','--cores','2','--no-out-link']).stdout.decode().strip()
    assert planner_cached == result, 'planner cache policy changed the resolved output'
    report['planner_substitution_policy']='passed'
    nested = out/'nested-command'
    write(nested/'go.mod', 'module example.com/nested\n\ngo 1.25\n')
    write(nested/'cmd/hello/main.go', 'package main\nimport ("fmt"; _ "net")\nfunc main(){fmt.Println("nested works")}\n')
    result = run(['nix-build',repo/'tests/dynamic-compatibility.nix','--argstr','system',system,
        '--argstr','toolchainAttr',a.flake_toolchain,'--arg','src',f'builtins.path {{ path = {json.dumps(str(nested))}; name = "source"; }}',
        '--argstr','package','./cmd/hello','--argstr','ldflags','--buildid=custom/ID',
        '--option','allow-import-from-derivation','false','--option','sandbox','true',
        '--option','sandbox-fallback','false','--max-jobs','4','--cores','2','--no-out-link']).stdout.decode().strip()
    executable = Path(result)/'bin/hello'
    assert run([executable]).stdout.decode() == 'nested works\n'
    assert run([tc/'bin/go','tool','buildid',executable],go=True).stdout.decode() == 'custom/ID\n'
    report['dynamic_nested_custom_buildid']='passed'
    print(version,'nested-command/custom-buildid/dynamic/IFD-disabled PASS',flush=True)
    # Exercise every new helper argument, including generated test-main sources
    # inside the recursive sandbox. Dynamic and standalone outputs must match.
    write(modes/'main_test.go','package main\nimport "testing"\nfunc TestValue(t *testing.T){if Value()!=expected {t.Fatal("wrong build mode")}}\n')
    for name, cgo, race in [('test-cgo-disabled','0',False), ('test-race-cgo','1',True)]:
        export = out/(name+'-export')
        flags = ['-test','-cgo',cgo] + (['-race'] if race else [])
        run([cli,'export','-C',modes,'-out',export,'-toolchain',tc,'-module-proxy','off',*flags,'.'])
        graph = json.loads((export/'graph.json').read_text())
        assert graph['cgo'] == cgo
        standalone = run(['nix-build',export,'-A','default','--no-out-link','--max-jobs','4','--cores','2']).stdout.decode().strip()
        dynamic = run(['nix-build',repo/'tests/dynamic-compatibility.nix','--argstr','system',system,
            '--argstr','toolchainAttr',a.flake_toolchain,'--arg','src',f'builtins.path {{ path = {json.dumps(str(modes))}; name = "source"; }}',
            '--arg','test','true','--arg','race',str(race).lower(),'--arg','cgoEnabled',str(cgo=='1').lower(),
            '--option','allow-import-from-derivation','false','--max-jobs','4','--cores','2','-o',out/(name+'-dynamic')]).stdout.decode().strip()
        assert Path(dynamic).resolve() == Path(standalone), (name, dynamic, standalone)
        output = run([Path(dynamic)/'bin'/graph['binaryName'],'-test.v'],cwd=modes).stdout.decode()
        assert 'PASS: TestValue' in output, output
        report.setdefault('dynamic_tests',[]).append({'name':name,'store_path':standalone,'cgo':cgo,'race':race})
        print(version,name,'dynamic/standalone/IFD-disabled PASS',flush=True)
    empty = out/'empty-tests'
    write(empty/'go.mod','module example.com/emptytests\ngo 1.25\n')
    write(empty/'sample.go','package sample\n')
    dynamic = run(['nix-build',repo/'tests/dynamic-compatibility.nix','--argstr','system',system,
        '--argstr','toolchainAttr',a.flake_toolchain,'--arg','src',f'builtins.path {{ path = {json.dumps(str(empty))}; name = "source"; }}',
        '--arg','test','true','--arg','allowEmptyTests','true',
        '--option','allow-import-from-derivation','false','--max-jobs','4','--cores','2','--no-out-link']).stdout.decode().strip()
    assert json.loads((Path(dynamic)/'nix-support/no-tests').read_text()) == {'version':1,'status':'no-tests'}
    assert not (Path(dynamic)/'bin').exists()
    report['dynamic_empty_tests']='passed'
report['incremental_cache_and_embeds']='passed'
report['test_binaries_and_race_detection']='passed'
(out/'results.json').write_text(json.dumps(report,indent=2)+'\n')
print(version,'incremental/cache/standalone PASS',flush=True)
