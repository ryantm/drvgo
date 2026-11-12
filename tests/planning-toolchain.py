#!/usr/bin/env python3
"""Check compact discovery, unchanged recipes, and full-SDK empty-test validation."""

import argparse
import json
from pathlib import Path
import subprocess
import tempfile


def nix_string(value):
    return json.dumps(str(value)).replace("${", r"\${")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--toolchain", type=Path, required=True)
    parser.add_argument("--drvgo", type=Path, required=True)
    parser.add_argument("--flake-toolchain", required=True)
    parser.add_argument("--out", type=Path)
    args = parser.parse_args()
    repo = Path(__file__).resolve().parent.parent
    out = (args.out or Path(tempfile.mkdtemp(prefix="drvgo-planning-toolchain-"))).resolve()
    out.mkdir(parents=True, exist_ok=True)
    if any(out.iterdir()):
        parser.error("--out must be empty")
    flags = ["--extra-experimental-features", "nix-command ca-derivations dynamic-derivations recursive-nix",
             "--option", "allow-import-from-derivation", "false", "--option", "sandbox", "true",
             "--option", "sandbox-fallback", "false", "--max-jobs", "2", "--cores", "2"]

    def run(command, label, failure=None):
        p = subprocess.run([str(x) for x in command], cwd=repo, capture_output=True, text=True)
        (out / (label + ".log")).write_text(p.stderr)
        if failure is not None:
            assert p.returncode != 0 and failure in p.stderr, (label, p.stderr)
        elif p.returncode:
            raise RuntimeError(f"{label}: {p.returncode}\n{p.stderr}")
        return p.stdout.strip()

    system = run(["nix", "eval", "--impure", "--raw", "--expr", "builtins.currentSystem"], "system")
    tc = args.toolchain.resolve()
    tc_expr = f'(builtins.getFlake {nix_string(repo)}).packages.{system}.{args.flake_toolchain}'
    sdk = Path(run(["nix", "eval", "--impure", "--raw", "--expr", tc_expr + ".go.outPath"], "sdk"))
    package = f'(import ./nix/dynamic.nix {{ toolchain = {tc_expr}; }})'
    profile_expr = f'({package} {{ src = ./tests/fixture; }}).planningToolchain'
    profile = Path(run(["nix", "build", *flags, "--impure", "--no-link", "--print-out-paths", "--expr", profile_expr], "profile"))
    closure = run(["nix-store", "--query", "--requisites", profile], "closure").splitlines()
    assert str(sdk) not in closure, closure
    preserved = {}
    for relative in ["src", "lib", "pkg/include", "bin/go", "go.env", "VERSION"]:
        label = relative.replace("/", "-")
        original_hash = run(["nix", "hash", "path", sdk / "share/go" / relative], label + "-original-hash")
        compact_hash = run(["nix", "hash", "path", profile / "share/go" / relative], label + "-compact-hash")
        assert original_hash == compact_hash, relative
        preserved[relative] = compact_hash
    target = run([sdk / "bin/go", "env", "GOOS", "GOARCH"], "target").replace("\n", "_")
    for tool in ["asm", "cgo", "compile", "link", "preprofile", "vet"]:
        original = sdk / "share/go/pkg/tool" / target / tool
        compact = profile / "share/go/pkg/tool" / target / tool
        assert run([original, "-V=full"], tool + "-original") == run([compact, "-V=full"], tool + "-compact")
        run([compact, "-o", out / "unexpected-output"], tool + "-reject", failure="only supports -V=full")
    assert not (out / "unexpected-output").exists()
    recipes = {}
    for label, planning in [("original", tc), ("compact", profile)]:
        graph = out / label
        run([args.drvgo.resolve(), "export", "-C", repo / "tests/fixture", "-out", graph,
             "-toolchain", planning, "-module-proxy", "off", "."], label + "-export")
        expression = f'builtins.mapAttrs (_: d: d.drvPath) (import (builtins.toPath {nix_string(graph)}) {{toolchain={tc_expr};}}).units'
        recipes[label] = json.loads(run(["nix-instantiate", "--eval", "--strict", "--read-write-mode", "--json", "--expr", expression], label + "-recipes"))
    assert recipes["original"] == recipes["compact"], recipes

    empty = out / "empty"
    empty.mkdir()
    (empty / "go.mod").write_text("module example.com/empty-planner\ngo 1.25\n")
    empty_expr = f'{package} {{ src = builtins.path {{ path = {nix_string(empty)}; name = "source"; }}; test = true; allowEmptyTests = true; }}'
    selected = run(["nix", "eval", *flags, "--impure", "--raw", "--expr", f'({empty_expr}).planningToolchain.outPath'], "empty-sdk")
    assert selected == str(tc), (selected, tc)
    pure_expr = empty_expr.replace("test = true;", "test = true; cgoEnabled = false;")
    selected = run(["nix", "eval", *flags, "--impure", "--raw", "--expr", f'({pure_expr}).planningToolchain.outPath'], "empty-pure-sdk")
    assert selected == str(sdk), (selected, sdk)
    # Listing succeeds, but actual compilation must reject this type error.
    (empty / "value.go").write_text("package sample\nvar Value int = false\n")
    run(["nix", "build", *flags, "--impure", "--no-link", "--expr", empty_expr], "empty-type-error", failure="cannot use false")
    # An excluded directory takes the compiled go/build selector path.
    (empty / "value.go").write_text("//go:build drvgo_excluded\n\npackage sample\n")
    marker = Path(run(["nix", "build", *flags, "--impure", "--no-link", "--print-out-paths", "--expr", empty_expr], "empty-excluded"))
    assert json.loads((marker / "nix-support/no-tests").read_text()) == {"version": 1, "status": "no-tests"}
    assert not (marker / "bin").exists()
    # A package without tests still needs its C code compiled and validated.
    native = 'package sample\n/* static int answer(void) { return 42; } */\nimport "C"\nfunc Value() int { return int(C.answer()) }\n'
    (empty / "value.go").write_text(native)
    native_marker = Path(run(["nix", "build", *flags, "--impure", "--no-link", "--print-out-paths", "--expr", empty_expr], "empty-native"))
    assert json.loads((native_marker / "nix-support/no-tests").read_text()) == {"version": 1, "status": "no-tests"}
    (empty / "value.go").write_text(native.replace("/* static", "/*\n#error DRVGO_CGO_VALIDATION\nstatic"))
    run(["nix", "build", *flags, "--impure", "--no-link", "--expr", empty_expr], "empty-native-error", failure="DRVGO_CGO_VALIDATION")
    report = {"profile": str(profile), "sdk": str(sdk), "closure": closure, "preserved": preserved,
              "identical_recipes": recipes["compact"], "version_metadata": "passed",
              "real_compilation_rejected": True, "empty_test_type_error_rejected": True,
              "empty_excluded_selector": str(marker), "empty_native_validation": str(native_marker),
              "empty_native_error_rejected": True}
    (out / "results.json").write_text(json.dumps(report, indent=2) + "\n")
    print("compact discovery/unchanged recipes/version metadata/full-SDK validation PASS", flush=True)


if __name__ == "__main__":
    main()
