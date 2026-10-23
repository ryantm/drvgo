#!/usr/bin/env python3
"""Differential build and cache tests for one explicitly selected Go toolchain."""
import argparse
import gzip
import json
import os
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
env = {k:v for k,v in os.environ.items() if not (k.startswith(('GO','CGO_','NIX_')) or k in ('CC','CXX','PATH'))}
env.update(PATH=str(tc/'bin')+':'+os.environ['PATH'], GOENV='off', GOFLAGS='', GOTOOLCHAIN='local', CGO_ENABLED='1',
           GOMAXPROCS='2', GOPROXY='off', CC=str(tc/'bin/gcc'), CXX=str(tc/'bin/g++'))

def run(cmd, cwd=None, go=False):
    p = subprocess.run([str(x) for x in cmd], cwd=cwd, env=env if go else None, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
    if p.returncode:
        raise RuntimeError(f'{cmd}: {p.returncode}\n{p.stdout.decode()}\n{p.stderr.decode()}')
    return p

def write(path, content):
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(content)

version = run([tc/'bin/go','env','GOVERSION'], go=True).stdout.decode().strip()
report = {'go':version, 'toolchain':str(tc), 'cases':[], 'nix':run(['nix','--version']).stdout.decode().strip()}

def case(name, source, expected, tags='', ldflags=''):
    native = out/(name+'-native')
    flags = ['-trimpath','-buildvcs=false','-tags',tags,'-ldflags',ldflags]
    run([tc/'bin/go','build',*flags,'-o',native,'.'], cwd=source, go=True)
    normal = run([native]).stdout.decode()
    assert normal == expected, (name,normal,expected)
    export = out/(name+'-export')
    run([cli,'export','-C',source,'-out',export,'-toolchain',tc,'-module-proxy','off','-tags',tags,'-ldflags',ldflags,'.'])
    graph = json.loads((export/'graph.json').read_text())
    result = run(['nix-build',export,'-A','default','--no-out-link','--max-jobs','4','--cores','2']).stdout.decode().strip()
    actual = run([Path(result)/'bin'/graph['binaryName']]).stdout.decode()
    assert actual == normal, (name,actual,normal)
    paths = json.loads(run(['nix-instantiate','--eval','--strict','--json','--expr',
        'builtins.mapAttrs (_: d: d.drvPath) (import '+str(export)+' {}).units']).stdout)
    report['cases'].append({'name':name,'output':actual,'units':len(graph['units']),
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
checkenv=dict(os.environ,DRVGO_TEST_TOOLCHAIN=str(tc))
p=subprocess.run([str(a.integration_test.resolve()),'-test.run=^TestEmbeddedAssetsAndCacheIsolation$','-test.v'],cwd=repo/'internal/export',env=checkenv,stdout=subprocess.PIPE,stderr=subprocess.STDOUT)
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
    report['dynamic_ifd_disabled']='passed'
    print(version,'dynamic/IFD-disabled PASS',flush=True)
report['incremental_cache_and_embeds']='passed'
(out/'results.json').write_text(json.dumps(report,indent=2)+'\n')
print(version,'incremental/cache/standalone PASS',flush=True)
