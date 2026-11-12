#!/usr/bin/env python3
"""Check compact compiler archives, private-edit cutoff, and native dynamic builds."""

import argparse
import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile


def nix_string(value):
    return json.dumps(str(value)).replace("${", r"\${")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--toolchain", type=Path, required=True)
    parser.add_argument("--drvgo", type=Path, required=True)
    parser.add_argument("--flake-toolchain", required=True)
    parser.add_argument("--iets", type=Path)
    parser.add_argument("--out", type=Path)
    parser.add_argument("--nix-option", nargs=2, action="append", default=[], metavar=("NAME", "VALUE"))
    args = parser.parse_args()
    repo = Path(__file__).resolve().parent.parent
    tc, cli = args.toolchain.resolve(), args.drvgo.resolve()
    out = (args.out or Path(tempfile.mkdtemp(prefix="drvgo-export-data-"))).resolve()
    out.mkdir(parents=True, exist_ok=True)
    if any(out.iterdir()):
        parser.error("--out must be empty")
    flags = ["--extra-experimental-features", "nix-command ca-derivations dynamic-derivations recursive-nix",
             "--option", "allow-import-from-derivation", "false", "--option", "sandbox", "true",
             "--option", "sandbox-fallback", "false", "--max-jobs", "4", "--cores", "2"]
    for name, value in args.nix_option:
        flags += ["--option", name, value]
    report = {"toolchain": str(tc), "flags": flags, "modes": {}}

    def run(command, label, env=None, failure=None):
        result = subprocess.run([str(x) for x in command], cwd=repo, env=env, capture_output=True, text=True)
        (out / (label + ".log")).write_text(result.stderr)
        if failure:
            assert result.returncode != 0 and failure in result.stderr, (label, result.stderr)
        elif result.returncode:
            raise RuntimeError(f"{label}: {result.returncode}\n{result.stderr}")
        return result.stdout.strip()

    def write(source, name, contents):
        path = source / name
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(contents)

    system = run(["nix", "eval", "--impure", "--raw", "--expr", "builtins.currentSystem"], "system")
    tc_expr = f'(builtins.getFlake {nix_string(repo)}).packages.{system}.{args.flake_toolchain}'
    for cutoff in [False, True]:
        mode = "cutoff" if cutoff else "interfaces"
        source = out / mode
        module = f"example.com/exportdata/{mode}"
        write(source, "go.mod", f"module {module}\ngo 1.25\n")
        initial = "package dep\nfunc Value() int { return privateValue() }\n//go:noinline\nfunc privateValue() int { return 42 }\n"
        write(source, "dep/value.go", initial)
        previous = "dep"
        for i in range(1, 6):
            package = f"layer{i}"
            write(source, f"{package}/value.go", f'package {package}\nimport "{module}/{previous}"\nfunc Value() int {{ return {previous}.Value() }}\n')
            previous = package
        write(source, "main.go", f'package main\nimport ("fmt"; "{module}/{previous}")\nfunc main() {{ fmt.Println({previous}.Value()) }}\n')
        options = f'exportData = true; earlyCutoff = {str(cutoff).lower()};'

        def exported(selected_source, label, failure=None, extra=()):
            graph = out / (mode + "-" + label + "-export")
            run([cli, "export", "-C", selected_source, "-out", graph, "-toolchain", tc, "-module-proxy", "off", *extra, "."], mode + "-" + label + "-export")
            units_expr = f'(import {graph} {{ {options} }}).units'
            drvs = json.loads(run(["nix-instantiate", *flags, "--eval", "--strict", "--json", "--expr",
                                  "builtins.mapAttrs (_: d: d.drvPath) " + units_expr], mode + "-" + label + "-eval"))
            expression = "builtins.concatMap (d: if d ? api then [d.out d.api] else [d]) (builtins.attrValues " + units_expr + ")"
            result = run(["nix", "build", *flags, "--impure", "--no-link", "--json", "--expr", expression], mode + "-" + label + "-build", failure=failure)
            if failure:
                return None
            built = {d["drvPath"]: d["outputs"] for d in json.loads(result)}
            data = json.loads((graph / "graph.json").read_text())
            outputs = {u["name"]: built[drvs[u["id"]]] for u in data["units"]}
            root = built[drvs[data["root"]]]["out"]
            return root, outputs, data["binaryName"]

        records = []
        for label, contents, expected in [
            ("initial", initial, "42"),
            ("private", initial.replace("return 42", "return 43"), "43"),
            ("inline", initial.replace("return 42", "return 43").replace("return privateValue()", "return privateValue()+1"), "44"),
        ]:
            write(source, "dep/value.go", contents)
            result, outputs, binary = exported(source, label)
            assert run([Path(result) / "bin" / binary], mode + "-" + label + "-run") == expected
            record = {"step": label, "result": result, "outputs": outputs}
            if records:
                changed = {name for name, value in outputs.items() if value["out"] != records[-1]["outputs"][name]["out"]}
                own = {name for name in outputs if name.startswith(module)}
                expected_changes = {module, module + "/dep"} if cutoff and label == "private" else own
                assert changed == expected_changes, (mode, label, changed, expected_changes)
                changed_api = {name for name, value in outputs.items() if value.get("api") != records[-1]["outputs"][name].get("api")}
                if cutoff and label == "private":
                    assert not changed_api, changed_api
                else:
                    assert changed_api == own - {module}, changed_api
                record.update(changed_archives=sorted(changed), changed_interfaces=sorted(changed_api))
            records.append(record)
        valid = (source / "dep/value.go").read_text()
        write(source, "dep/value.go", valid.replace("Value() int { return privateValue()+1", "Value() int64 { return int64(privateValue()+1)"))
        exported(source, "signature", failure="cannot use dep.Value()")
        write(source, "dep/value.go", valid)
        report["modes"][mode] = {"steps": records, "signature_change_rejected": True}

        native = out / (mode + "-native")
        shutil.copytree(repo / "tests/fixture", native)
        native_result, _, native_binary = exported(native, "native")
        assert run([Path(native_result) / "bin" / native_binary], mode + "-native-run") == "hello embedded world\nstable\nnested asset\n01 02 ff\nhidden asset"
        dynamic_expr = (f'import ./tests/dynamic-compatibility.nix {{ system = {nix_string(system)}; '
                        f'toolchainAttr = {nix_string(args.flake_toolchain)}; src = {native}; {options} }}')
        built = json.loads(run(["nix", "build", *flags, "--impure", "--no-link", "--json", "--expr", dynamic_expr], mode + "-dynamic"))
        assert str(Path(built[0]["outputs"]["out"]).resolve()) == native_result
        report["modes"][mode]["native_standalone_dynamic"] = native_result

        write(native, "main_test.go", 'package main\nimport "testing"\nfunc TestEmbeddedNative(t *testing.T) { main() }\n')
        write(native, "external_test.go", 'package main_test\nimport ("testing"; "example.com/libnix-fixture/dep")\n'
              'func TestExternal(t *testing.T) { if dep.Value() != "stable\\n" { t.Fatal("wrong dependency") } }\n')
        for race in [False, True]:
            label = "test-race" if race else "test"
            test_result, _, test_binary = exported(native, label, extra=["-test", *(["-race"] if race else [])])
            test_expression = dynamic_expr.replace(options, options + f" test = true; race = {str(race).lower()};")
            built = json.loads(run(["nix", "build", *flags, "--impure", "--no-link", "--json", "--expr", test_expression], mode + "-" + label + "-dynamic"))
            assert str(Path(built[0]["outputs"]["out"]).resolve()) == test_result
            output = run([Path(test_result) / "bin" / test_binary, "-test.v"], mode + "-" + label + "-run")
            assert "PASS: TestEmbeddedNative" in output and "PASS: TestExternal" in output, output
            report["modes"][mode][label] = test_result

        # An inline function can carry a runtime store path through its export
        # data. Both archive outputs must retain that input when copied alone.
        references = json.loads(run(["nix-instantiate", "--eval", "--strict", "--read-write-mode", "--json", "--expr",
            '{ used = builtins.toFile "drvgo-interface-data" "interface data"; '
            'unused = builtins.toFile "drvgo-interface-unused" "unused"; }'], mode + "-runtime-data"))
        runtime_source = out / (mode + "-runtime")
        runtime_module = module + "/runtime"
        write(runtime_source, "go.mod", f"module {runtime_module}\ngo 1.25\n")
        write(runtime_source, "dep/path.go", 'package dep\nfunc Path() string { return ' + json.dumps(references["used"]) + ' }\n')
        write(runtime_source, "dep/excluded.go", '//go:build drvgo_unused\n\npackage dep\nconst Unused = ' + json.dumps(references["unused"]) + '\n')
        write(runtime_source, "main.go", 'package main\nimport ("fmt"; "os"; "' + runtime_module + '/dep")\n'
              'func main() { data, err := os.ReadFile(dep.Path()); if err != nil { panic(err) }; fmt.Println(string(data)) }\n')
        runtime_result, runtime_outputs, runtime_binary = exported(runtime_source, "runtime")
        assert run([Path(runtime_result) / "bin" / runtime_binary], mode + "-runtime-run") == "interface data"
        dependency = runtime_outputs[runtime_module + "/dep"]
        for kind, path in {"binary": runtime_result, **dependency}.items():
            closure = run(["nix-store", "--query", "--requisites", path], mode + "-runtime-" + kind + "-closure").splitlines()
            assert references["used"] in closure and references["unused"] not in closure, (mode, kind, closure)
        cache = out / (mode + "-api-cache")
        run(["nix", "copy", "--to", cache.as_uri(), dependency["api"]], mode + "-api-copy")
        copied = {line.removeprefix("StorePath: ") for info in cache.glob("*.narinfo")
                  for line in info.read_text().splitlines() if line.startswith("StorePath: ")}
        assert copied == {dependency["api"], references["used"]}, copied
        report["modes"][mode]["runtime_reference_copy"] = sorted(copied)

        if args.iets:
            iets = args.iets.resolve()
            env = dict(os.environ, IETS_NIX_FALLBACK="never", IETS_PROGRESS="false", NIX_REMOTE="daemon")
            for nested in [False, True]:
                label = mode + ("-iets-nested" if nested else "-iets-outer")
                expression = (f'import ./tests/iets.nix {{ iets = builtins.storePath {nix_string(iets.parent.parent)}; '
                              f'src = {native}; baseToolchain = {tc_expr}; nested = {str(nested).lower()}; {options} }}')
                expected_drv = run(["nix-instantiate", *flags, "--eval", "--raw", "--read-write-mode", "--expr", f'({expression}).drvPath'], label + "-nix-eval")
                actual_drv = run([iets, "eval", "--impure", "--no-tracing-cache", "--raw", "--expr", f'({expression}).drvPath'], label + "-eval", env=env)
                assert actual_drv == expected_drv
                result = run([iets, "build", "--impure", "--no-tracing-cache", "--option", "allow-import-from-derivation", "false", "--expr", expression], label + "-build", env=env)
                assert str(Path(result).resolve()) == native_result
                report["modes"][mode][label] = result
        (out / "results.json").write_text(json.dumps(report, indent=2) + "\n")
        print(mode, "private/inline/signature/native/dynamic PASS", flush=True)


if __name__ == "__main__":
    main()
