#!/usr/bin/env python3
"""Compare cache reuse when an identical cgo checkout moves to a new directory."""

import argparse
import json
from pathlib import Path
import re
import shutil
import statistics
import subprocess
import tempfile
import time
import uuid


def nix_string(value):
    return json.dumps(str(value)).replace("${", r"\${")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    for label in ["before", "after"]:
        parser.add_argument(f"--{label}-drvgo", type=Path, required=True)
        parser.add_argument(f"--{label}-toolchain", type=Path, required=True)
    parser.add_argument("--runs", type=int, default=5)
    parser.add_argument("--out", type=Path)
    args = parser.parse_args()
    if args.runs < 1:
        parser.error("--runs must be positive")
    out = (args.out or Path(tempfile.mkdtemp(prefix="drvgo-relocation-"))).resolve()
    out.mkdir(parents=True, exist_ok=True)
    if any(out.iterdir()):
        parser.error("--out must be empty")
    tools = {label: {"drvgo": str(getattr(args, label + "_drvgo").resolve()),
                     "toolchain": str(getattr(args, label + "_toolchain").resolve())}
             for label in ["before", "after"]}
    flags = ["--option", "allow-import-from-derivation", "false", "--option", "sandbox", "true",
             "--option", "sandbox-fallback", "false", "--option", "substituters", "",
             "--option", "always-allow-substitutes", "false", "--max-jobs", "4", "--cores", "2"]
    nonce = uuid.uuid4().hex[:12]
    report = {"scope": "Export, evaluate, and realize an identical cgo checkout at a second location; first location built immediately beforehand; warm tools and dependencies; no remote lookups. Each revision/trial has a distinct module path.",
              "tools": tools, "flags": flags, "nonce": nonce, "records": []}

    def build(label, source, name):
        graph = out / (name + "-export")
        log_path = out / (name + ".log")
        with log_path.open("w") as log:
            start = time.monotonic()
            subprocess.run([tools[label]["drvgo"], "export", "-C", str(source), "-out", str(graph),
                            "-toolchain", tools[label]["toolchain"], "-module-proxy", "off", "."],
                           stdout=log, stderr=log, check=True)
            exported = time.monotonic()
            expression = f'(import (builtins.toPath {nix_string(graph)}) {{}}).default'
            drv = subprocess.check_output(["nix-instantiate", *flags, "--expr", expression], stderr=log, text=True).strip()
            evaluated = time.monotonic()
            result = subprocess.check_output(["nix-store", *flags, "--realise", drv], stderr=log, text=True).strip()
            end = time.monotonic()
        assert subprocess.check_output([str(Path(result) / "bin/relocated")], text=True).strip() == "42"
        units = json.loads(subprocess.check_output([
            "nix-instantiate", "--eval", "--strict", "--json", "--expr",
            f'builtins.mapAttrs (_: d: d.drvPath) (import (builtins.toPath {nix_string(graph)}) {{}}).units'], text=True))
        graph_units = json.loads((graph / "graph.json").read_text())["units"]
        units = {u["name"]: units[u["id"]] for u in graph_units}
        builds = sorted(set(re.findall(r"building '(/nix/store/[^'\n]+-go-[^'\n]+\.drv)'", log_path.read_text())))
        return dict(export=exported-start, evaluation=evaluated-exported, realization=end-evaluated,
                    total=end-start, result=result, derivation=drv, units=units, package_builds=builds)

    for trial in range(args.runs + 1):
        for label in (["before", "after"] if trial % 2 == 0 else ["after", "before"]):
            name = f"{label}-{trial}"
            # A distinct module path prevents reuse from another trial's first
            # location while leaving the common standard library warm.
            module = f"example.com/{nonce}/{label}/{trial}/relocated"
            source = out / (name + "-first")
            (source / "native").mkdir(parents=True)
            (source / "go.mod").write_text(f"module {module}\ngo 1.25\n")
            (source / "main.go").write_text('package main\n/*\n#cgo CFLAGS: -I.\n#include "value.h"\n*/\nimport "C"\n'
                                             + f'import ("fmt"; "{module}/native")\n'
                                             + 'func main(){fmt.Println(int(C.answer())+native.Value())}\n')
            (source / "value.h").write_text("static int answer(void) { return 20; }\n")
            (source / "native/value.h").write_text("static int answer(void) { return 22; }\n")
            (source / "native/native.go").write_text('package native\n/*\n#cgo CFLAGS: -I.\n#include "value.h"\n*/\nimport "C"\nfunc Value() int { return int(C.answer()); }\n')
            relocated = out / (name + "-second")
            shutil.copytree(source, relocated)
            initial = build(label, source, name + "-first")
            second = build(label, relocated, name + "-second")
            changed = sorted(k for k, v in second["units"].items() if initial["units"].get(k) != v)
            record = dict(label=label, trial=trial, warmup=trial == 0, initial=initial, relocated=second,
                          changed_units=changed, reused_output=initial["result"] == second["result"])
            report["records"].append(record)
            (out / "results.json").write_text(json.dumps(report, indent=2) + "\n")
            print(label, trial, f'{second["total"]:.3f}s', len(changed), "changed recipes;",
                  len(second["package_builds"]), "package builds", flush=True)
    report["medians"] = {label: {phase: statistics.median(r["relocated"][phase] for r in report["records"]
                            if r["label"] == label and not r["warmup"])
                            for phase in ["export", "evaluation", "realization", "total"]} for label in tools}
    (out / "results.json").write_text(json.dumps(report, indent=2) + "\n")
    print(json.dumps(report["medians"], indent=2))


if __name__ == "__main__":
    main()
