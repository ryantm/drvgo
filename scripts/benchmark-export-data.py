#!/usr/bin/env python3
"""Measure private-edit rebuilds with full archives, interfaces and early cutoff."""

import argparse
import json
from pathlib import Path
import re
import statistics
import subprocess
import tempfile
import time
import uuid


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--drvgo", type=Path, required=True)
    parser.add_argument("--toolchain", type=Path, required=True)
    parser.add_argument("--runs", type=int, default=5)
    parser.add_argument("--layers", type=int, default=5)
    parser.add_argument("--out", type=Path)
    args = parser.parse_args()
    if args.runs < 1 or args.layers < 1:
        parser.error("--runs and --layers must be positive")
    cli, tc = args.drvgo.resolve(), args.toolchain.resolve()
    out = (args.out or Path(tempfile.mkdtemp(prefix="drvgo-export-data-benchmark-"))).resolve()
    out.mkdir(parents=True, exist_ok=True)
    if any(out.iterdir()):
        parser.error("--out must be empty")
    flags = ["--extra-experimental-features", "nix-command ca-derivations",
             "--option", "allow-import-from-derivation", "false", "--option", "sandbox", "true",
             "--option", "sandbox-fallback", "false", "--option", "substituters", "",
             "--option", "always-allow-substitutes", "false", "--max-jobs", "4", "--cores", "2"]
    modes = {"full": "", "interfaces": "exportData = true;",
             "cutoff": "exportData = true; earlyCutoff = true;"}
    nonce = uuid.uuid4().hex[:12]
    report = {"scope": "Export, evaluate and realize a private implementation edit; warm tools, standard library and initial build; no remote lookups. Each mode/trial has a distinct module path. Initial builds are recorded but excluded from private-edit medians.",
              "drvgo": str(cli), "toolchain": str(tc), "flags": flags,
              "layers": args.layers, "nonce": nonce, "records": []}

    def write(source, name, text):
        path = source / name
        path.parent.mkdir(parents=True, exist_ok=True)
        path.write_text(text)

    def build(mode, source, name, expected):
        graph = out / (name + "-export")
        log_path = out / (name + ".log")
        with log_path.open("w") as log:
            start = time.monotonic()
            subprocess.run([cli, "export", "-C", source, "-out", graph, "-toolchain", tc,
                            "-module-proxy", "off", "."], stdout=log, stderr=log, check=True)
            exported = time.monotonic()
            graph_path = json.dumps(str(graph)).replace('${', r'\${')
            expression = f'(import (builtins.toPath {graph_path}) {{ {modes[mode]} }}).default'
            drv = subprocess.check_output(["nix-instantiate", *flags, "--expr", expression], stderr=log, text=True).strip()
            evaluated = time.monotonic()
            outputs = json.loads(subprocess.check_output(["nix", "build", *flags, "--no-link", "--json", drv + "^out"], stderr=log, text=True))
            end = time.monotonic()
        result = outputs[0]["outputs"]["out"]
        assert subprocess.check_output([Path(result) / "bin/privateedit"], text=True).strip() == expected
        builds = sorted(set(re.findall(r"building '(/nix/store/[^'\n]+-go-[^'\n]+\.drv)'", log_path.read_text())))
        return dict(export=exported-start, evaluation=evaluated-exported, realization=end-evaluated,
                    total=end-start, result=result, derivation=drv, package_builds=builds)

    for trial in range(args.runs + 1):
        order = list(modes)
        order = order[trial % len(order):] + order[:trial % len(order)]
        for mode in order:
            name = f"{mode}-{trial}"
            source = out / name
            module = f"example.com/{nonce}/{name}/privateedit"
            write(source, "go.mod", f"module {module}\ngo 1.25\n")
            initial = "package dep\nfunc Value() int { return privateValue() }\n//go:noinline\nfunc privateValue() int { return 42 }\n"
            write(source, "dep/value.go", initial)
            previous = "dep"
            for i in range(1, args.layers + 1):
                package = f"layer{i}"
                write(source, f"{package}/value.go", f'package {package}\nimport "{module}/{previous}"\nfunc Value() int {{ return {previous}.Value() }}\n')
                previous = package
            write(source, "main.go", f'package main\nimport ("fmt"; "{module}/{previous}")\nfunc main() {{ fmt.Println({previous}.Value()) }}\n')
            first = build(mode, source, name + "-initial", "42")
            write(source, "dep/value.go", initial.replace("return 42", "return 43"))
            changed = build(mode, source, name + "-private", "43")
            expected = 2 if mode == "cutoff" else args.layers + 2
            assert len(changed["package_builds"]) == expected, (mode, changed)
            report["records"].append(dict(mode=mode, trial=trial, warmup=trial == 0,
                                           initial=first, private=changed))
            (out / "results.json").write_text(json.dumps(report, indent=2) + "\n")
            print(mode, trial, f'{changed["total"]:.3f}s', expected, "package builds", flush=True)
    report["medians"] = {mode: {phase: statistics.median(r["private"][phase] for r in report["records"]
                                  if r["mode"] == mode and not r["warmup"])
                                  for phase in ["export", "evaluation", "realization", "total"]} for mode in modes}
    (out / "results.json").write_text(json.dumps(report, indent=2) + "\n")
    print(json.dumps(report["medians"], indent=2))


if __name__ == "__main__":
    main()
