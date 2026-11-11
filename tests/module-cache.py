#!/usr/bin/env python3
"""Exercise immutable external modules, including cgo, without network access."""

import argparse
import json
import os
from pathlib import Path
import platform
import subprocess
import tempfile
import zipfile


def nix_string(value):
    return json.dumps(str(value)).replace("${", r"\${")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--toolchain", type=Path, required=True)
    parser.add_argument("--drvgo", type=Path, required=True)
    parser.add_argument("--flake-toolchain", required=True)
    parser.add_argument("--iets", type=Path)
    parser.add_argument("--out", type=Path)
    args = parser.parse_args()
    repo = Path(__file__).resolve().parent.parent
    tc, cli = args.toolchain.resolve(), args.drvgo.resolve()
    out = (args.out or Path(tempfile.mkdtemp(prefix="drvgo-module-cache-"))).resolve()
    out.mkdir(parents=True, exist_ok=True)
    if any(out.iterdir()):
        parser.error("--out must be empty")
    features = ["--extra-experimental-features", "ca-derivations dynamic-derivations recursive-nix"]
    flags = [*features, "--option", "allow-import-from-derivation", "false",
             "--option", "sandbox", "true", "--option", "sandbox-fallback", "false",
             "--max-jobs", "2", "--cores", "2"]
    report = {}

    def run(command, label, cwd=repo, env=None, failure=None):
        result = subprocess.run([str(x) for x in command], cwd=cwd, env=env, capture_output=True, text=True)
        (out / (label + ".log")).write_text(result.stderr)
        if failure is not None:
            assert result.returncode != 0 and failure in result.stderr, (label, result.stderr)
        elif result.returncode:
            raise RuntimeError(f"{label}: {result.returncode}\n{result.stderr}")
        return result.stdout.strip()

    proxy = out / "proxy"
    version_dir = proxy / "example.com/!upper/dep/@v"
    version_dir.mkdir(parents=True)
    mod = "module example.com/Upper/dep\ngo 1.23\n"
    (version_dir / "v1.0.0.mod").write_text(mod)
    (version_dir / "v1.0.0.info").write_text('{"Version":"v1.0.0","Time":"2020-01-01T00:00:00Z"}\n')
    (version_dir / "list").write_text("v1.0.0\n")
    files = {"go.mod": mod, "config.h": "#define ANSWER 42\n",
             "dep.go": 'package dep\n/*\n#cgo CFLAGS: -I.\n#include "config.h"\nstatic int answer(void) { return ANSWER; }\n*/\nimport "C"\nfunc Value() int { return int(C.answer()) }\n'}
    with zipfile.ZipFile(version_dir / "v1.0.0.zip", "w", compression=zipfile.ZIP_DEFLATED) as archive:
        for name, content in files.items():
            entry = zipfile.ZipInfo("example.com/Upper/dep@v1.0.0/" + name, date_time=(2020, 1, 1, 0, 0, 0))
            archive.writestr(entry, content)
    system = run(["nix", "eval", "--impure", "--raw", "--expr", "builtins.currentSystem"], "system")
    tc_expr = f'(builtins.getFlake {nix_string(repo)}).packages.{system}.{args.flake_toolchain}'
    cache_expr = (f'let tc = {tc_expr}; in import ./nix/module-cache.nix {{ pkgs = tc.pkgs; go = tc.go; '
                  f'drvgo = import ./nix/package.nix {{ pkgs = tc.pkgs; }}; '
                  f'modules = builtins.path {{ path = {nix_string(proxy)}; name = "module-proxy"; }}; }}')
    cache = Path(run(["nix-build", *flags, "--no-out-link", "--expr", cache_expr], "prepare"))
    # Classic --check needs the resolved recipe for floating CA outputs.
    cache_drv = run(["nix-store", "--query", "--deriver", cache], "cache-deriver")
    run(["nix-store", *flags, "--realise", "--check", cache_drv], "prepare-reproducible")
    assert not run(["nix-store", "--query", "--references", cache], "cache-references")
    assert not list(cache.rglob("*.zip")) and not list(cache.rglob("*.lock"))
    assert (cache / "cache/download/example.com/!upper/dep/@v/v1.0.0.ziphash").is_file()
    report["cache"] = str(cache)

    source = out / "source"
    source.mkdir()
    (source / "go.mod").write_text("module example.com/cache-app\ngo 1.23\nrequire example.com/Upper/dep v1.0.0\n")
    (source / "main.go").write_text('package main\nimport("fmt"; dep "example.com/Upper/dep")\nfunc main(){fmt.Println(dep.Value(), "original")}\n')
    env = {k: v for k, v in os.environ.items()
           if not k.startswith(("GO", "CGO_", "NIX_", "DEVELOPER_DIR_", "MACOSX_DEPLOYMENT_TARGET_"))
           and k not in ("CC", "CXX", "SDKROOT", "DEVELOPER_DIR", "MACOSX_DEPLOYMENT_TARGET")}
    cc, cxx = ("clang", "clang++") if platform.system() == "Darwin" else ("gcc", "g++")
    env.update(PATH=str(tc / "bin") + ":" + os.environ["PATH"], GOENV="off", GOWORK="off", GOFLAGS="",
               GOTOOLCHAIN="local", GOPROXY="off", GOSUMDB="off", GOMODCACHE=str(cache), GOMAXPROCS="2",
               GOCACHE=str(out / "go-cache"), CGO_ENABLED="1", CC=str(tc / "bin" / cc), CXX=str(tc / "bin" / cxx))
    # This also writes the application's correct go.sum using the retained hashes.
    run([tc / "bin/go", "build", "-mod=mod", "-trimpath", "-buildvcs=false", "-o", out / "native", "."],
        "native-build", cwd=source, env=env)
    assert run([out / "native"], "native-run") == "42 original"

    def exported(label, selected_cache=cache):
        graph = out / (label + "-export")
        export_env = None
        module_args = ["-module-cache", selected_cache]
        if selected_cache is None:
            # An isolated GOPATH proves the proxy is extracted at a different
            # location, without accidentally reusing a process-global cache.
            temporary = out / (label + "-temporary")
            temporary.mkdir()
            export_env = dict(os.environ, TMPDIR=str(temporary))
            module_args = ["-module-proxy", proxy.as_uri()]
        run([cli, "export", "-C", source, "-out", graph, "-toolchain", tc, *module_args, "."],
            label + "-export", env=export_env)
        result = run(["nix-build", *flags, "--no-out-link", graph, "-A", "default"], label + "-build")
        paths = json.loads(run(["nix-instantiate", *features, "--eval", "--strict", "--json", "--expr",
                               f'builtins.mapAttrs (_: d: d.drvPath) (import {graph} {{}}).units'], label + "-units"))
        units = json.loads((graph / "graph.json").read_text())["units"]
        return result, {unit["name"]: paths[unit["id"]] for unit in units}

    original, units = exported("original")
    alternate_cache = run(["nix-instantiate", "--eval", "--raw", "--read-write-mode", "--expr",
                           f'builtins.path {{ path = {cache}; name = "alternate-module-cache"; }}'], "alternate-cache")
    assert alternate_cache != str(cache)
    alternate, alternate_units = exported("alternate-cache", alternate_cache)
    assert alternate_units == units and alternate == original, "module-cache relocation changed recipes"
    report["module_cache_relocation"] = True
    from_proxy, proxy_units = exported("fresh-proxy", None)
    assert proxy_units == units and from_proxy == original, "fresh proxy extraction changed recipes"
    report["proxy_prepared_cache_reuse"] = True
    # Explicit cache overrides work with vendorHash=null and prepareModules=false.
    dynamic_expr = (f'import ./tests/dynamic-compatibility.nix {{ system = {nix_string(system)}; '
                    f'toolchainAttr = {nix_string(args.flake_toolchain)}; src = {source}; '
                    f'moduleCache = ({cache_expr}); prepareModules = false; }}')
    dynamic = run(["nix-build", *flags, "--no-out-link", "--expr", dynamic_expr], "dynamic")
    assert str(Path(dynamic).resolve()) == original, "external cgo modules changed between standalone and dynamic planning"
    assert run([Path(dynamic) / "bin/cache-app"], "dynamic-run") == "42 original"
    closure = run(["nix-store", "--query", "--requisites", dynamic], "runtime-closure").splitlines()
    assert str(cache) not in closure
    report["standalone_dynamic_output"] = original

    if args.iets:
        iets = args.iets.resolve()
        iets_env = dict(os.environ, IETS_NIX_FALLBACK="never", IETS_PROGRESS="false", NIX_REMOTE="daemon")
        for nested in [False, True]:
            label = "iets-nested" if nested else "iets-outer"
            expr = (f'import ./tests/iets.nix {{ iets = builtins.storePath {nix_string(iets.parent.parent)}; '
                    f'src = {source}; moduleCache = ({cache_expr}); baseToolchain = {tc_expr}; '
                    f'nested = {str(nested).lower()}; }}')
            nix_drv = run(["nix-instantiate", *flags, "--eval", "--raw", "--read-write-mode", "--expr", f'({expr}).drvPath'], label + "-nix-eval")
            iets_drv = run([iets, "eval", "--impure", "--no-tracing-cache", "--raw", "-E", f'({expr}).drvPath'], label + "-eval", env=iets_env)
            assert nix_drv == iets_drv
            result = run([iets, "build", "--impure", "--no-tracing-cache", "--option", "allow-import-from-derivation", "false", "-E", expr], label + "-build", env=iets_env)
            assert Path(result).resolve() == Path(original)
            report[label] = result

    (source / "main.go").write_text((source / "main.go").read_text().replace('"original"', '"edited"'))
    edited, edited_units = exported("edited")
    assert edited != original
    assert {name for name in units if units[name] != edited_units[name]} == {"example.com/cache-app"}
    dynamic = run(["nix-build", *flags, "--no-out-link", "--expr", dynamic_expr], "edited-dynamic")
    assert str(Path(dynamic).resolve()) == edited
    assert run([Path(dynamic) / "bin/cache-app"], "edited-run") == "42 edited"
    report["source_edit_reuses_dependencies"] = True

    (source / "go.sum").write_text("example.com/Upper/dep v1.0.0 h1:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=\n")
    run(["nix-build", *flags, "--no-out-link", "--expr", dynamic_expr], "invalid-checksum", failure="checksum mismatch")
    report["invalid_checksum_rejected"] = True
    (out / "results.json").write_text(json.dumps(report, indent=2) + "\n")
    print("module-cache offline/cgo/dynamic/reuse/checksum PASS", flush=True)


if __name__ == "__main__":
    main()
