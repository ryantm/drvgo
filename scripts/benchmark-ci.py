#!/usr/bin/env python3
"""Compare dynamic CI builds with warm package caches and fresh planning each run."""

import argparse
import json
from pathlib import Path
import statistics
import subprocess
import tempfile
import time
import uuid


def nix_string(value):
    return json.dumps(str(value)).replace("${", r"\${")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--before", type=Path, required=True, help="baseline drvgo checkout")
    parser.add_argument("--after", type=Path, required=True, help="modified drvgo checkout")
    parser.add_argument("--source", type=Path, required=True, help="Go source tree; vendorHash=null")
    parser.add_argument("--package", default=".")
    parser.add_argument("--cgo", choices=["0", "1"], help="explicitly select cgo mode")
    parser.add_argument("--test", action="store_true", help="export a test binary")
    parser.add_argument("--allow-empty-tests", action="store_true")
    parser.add_argument("--tags", default="")
    parser.add_argument("--runs", type=int, default=5)
    parser.add_argument("--nix-option", nargs=2, action="append", default=[], metavar=("NAME", "VALUE"),
                        help="per-command Nix setting, applied equally to both revisions")
    parser.add_argument("--out", type=Path)
    args = parser.parse_args()
    if args.runs < 1:
        parser.error("--runs must be positive")
    if args.allow_empty_tests and not args.test:
        parser.error("--allow-empty-tests requires --test")
    out = (args.out or Path(tempfile.mkdtemp(prefix="drvgo-ci-benchmark-"))).resolve()
    out.mkdir(parents=True, exist_ok=True)
    if any(out.iterdir()):
        parser.error("--out must be an empty directory")
    flags = ["--extra-experimental-features", "ca-derivations dynamic-derivations recursive-nix",
             "--option", "allow-import-from-derivation", "false", "--max-jobs", "4", "--cores", "2"]
    for name, value in args.nix_option:
        flags.extend(["--option", name, value])
    roots = {"before": args.before.resolve(), "after": args.after.resolve()}
    build_options = ""
    if args.cgo is not None:
        build_options += f" cgoEnabled = {str(args.cgo == '1').lower()};"
    if args.test:
        build_options += " test = true;"
    if args.allow_empty_tests:
        build_options += " allowEmptyTests = true;"
    if args.tags:
        build_options += f" tags = {nix_string(args.tags)};"
    nonce = uuid.uuid4().hex[:12]
    report = {"scope": "dynamic evaluation + realization; fresh planner, warm tools and Go package outputs; cache metadata queries follow the selected Nix settings",
              "source": str(args.source.resolve()), "package": args.package,
              "cgo": args.cgo, "test": args.test, "allow_empty_tests": args.allow_empty_tests, "tags": args.tags,
              "nix": subprocess.check_output(["nix", "--version"], text=True).strip(),
              "roots": {k: str(v) for k, v in roots.items()}, "flags": flags, "nonce": nonce, "records": []}

    def run(label, trial):
        # The unique forwarding name forces planning even with an unchanged
        # source. Package recipes do not depend on this name and stay cached.
        name = f"drvgo-ci-{nonce}-{label}-{trial}"
        expression = (f"(import (builtins.toPath {nix_string(roots[label] / 'nix/dynamic.nix')}) {{}}) {{"
                      f"name = {nix_string(name)}; src = builtins.path {{ path = {nix_string(args.source.resolve())}; name = \"source\"; }}; "
                      f"package = {nix_string(args.package)};{build_options} }}")
        with (out / f"{label}-{trial}.log").open("w") as log:
            start = time.monotonic()
            drv = subprocess.check_output(["nix-instantiate", *flags, "--expr", expression], stderr=log, text=True).strip()
            evaluated = time.monotonic()
            result = subprocess.check_output(["nix-store", *flags, "--realise", drv], stderr=log, text=True).strip()
            end = time.monotonic()
        report["records"].append({"label": label, "trial": trial, "warmup": trial == 0,
                                  "evaluation": evaluated-start, "realization": end-evaluated, "total": end-start,
                                  "derivation": drv, "result": result, "command_output": str(Path(result).resolve())})
        (out / "results.json").write_text(json.dumps(report, indent=2) + "\n")
        print(f"{label} {trial}: evaluation {evaluated-start:.3f}s; realization {end-evaluated:.3f}s", flush=True)

    for trial in range(args.runs + 1):
        for label in (["before", "after"] if trial % 2 == 0 else ["after", "before"]):
            run(label, trial)
    for label in roots:
        outputs = {r["command_output"] for r in report["records"] if r["label"] == label}
        if len(outputs) != 1:
            raise RuntimeError(f"{label}: package outputs changed between trials: {outputs}")
    report["medians"] = {
        label: {phase: statistics.median(r[phase] for r in report["records"]
                                        if r["label"] == label and not r["warmup"])
                for phase in ["evaluation", "realization", "total"]}
        for label in roots
    }
    (out / "results.json").write_text(json.dumps(report, indent=2) + "\n")
    print(json.dumps(report["medians"], indent=2))
    print(f"Evidence: {out}")


if __name__ == "__main__":
    main()
