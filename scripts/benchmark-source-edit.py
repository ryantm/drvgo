#!/usr/bin/env python3
"""Compare dynamic rebuilds after application-only source edits."""

import argparse
import json
from pathlib import Path
import re
import statistics
import subprocess
import tempfile
import time
import uuid


def nix_string(value):
    return json.dumps(str(value)).replace("${", r"\${")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--before", type=Path, required=True)
    parser.add_argument("--after", type=Path, required=True)
    parser.add_argument("--runs", type=int, default=5)
    parser.add_argument("--out", type=Path)
    args = parser.parse_args()
    if args.runs < 1:
        parser.error("--runs must be positive")
    out = (args.out or Path(tempfile.mkdtemp(prefix="drvgo-source-edit-"))).resolve()
    out.mkdir(parents=True, exist_ok=True)
    if any(out.iterdir()):
        parser.error("--out must be empty")
    source = out / "source"
    source.mkdir()
    (source / "go.mod").write_text("module example.com/source-edit\ngo 1.25\n")
    roots = {"before": args.before.resolve(), "after": args.after.resolve()}
    flags = ["--extra-experimental-features", "ca-derivations dynamic-derivations recursive-nix",
             "--option", "allow-import-from-derivation", "false", "--option", "sandbox", "true",
             "--option", "sandbox-fallback", "false", "--option", "substituters", "",
             "--option", "always-allow-substitutes", "false", "--max-jobs", "4", "--cores", "2"]
    nonce = uuid.uuid4().hex[:12]
    report = {"scope": "Dynamic evaluation and realization after each main.go edit; warm tools and dependency outputs; fresh planning and unique application output per revision/trial; no remote lookups.",
              "roots": {k: str(v) for k, v in roots.items()}, "nonce": nonce, "flags": flags, "records": []}
    for trial in range(args.runs + 1):
        for label in (["before", "after"] if trial % 2 == 0 else ["after", "before"]):
            # Equal-length but distinct edits prevent the second revision from
            # reusing the first revision's just-built application output.
            value = f"{nonce}-{'B' if label == 'before' else 'A'}-{trial}"
            (source / "main.go").write_text('package main\nimport "fmt"\nfunc main(){fmt.Println(' + json.dumps(value) + ')}\n')
            expr = (f'(import {nix_string(roots[label] / "nix/dynamic.nix")} {{}}) {{ '
                    f'src = builtins.path {{ path = {nix_string(source)}; name = "source"; }}; '
                    f'name = "drvgo-edit-{nonce}-{label}-{trial}"; }}')
            log_path = out / f"{label}-{trial}.log"
            with log_path.open("w") as log:
                start = time.monotonic()
                drv = subprocess.check_output(["nix-instantiate", *flags, "--expr", expr], stderr=log, text=True).strip()
                evaluated = time.monotonic()
                result = subprocess.check_output(["nix-store", *flags, "--realise", drv], stderr=log, text=True).strip()
                end = time.monotonic()
            assert subprocess.check_output([str(Path(result) / "bin/source-edit")], text=True).strip() == value
            builds = sorted(set(re.findall(r"building '(/nix/store/[^'\n]+-go-[^'\n]+\.drv)'", log_path.read_text())))
            record = dict(label=label, trial=trial, warmup=trial == 0, evaluation=evaluated-start,
                          realization=end-evaluated, total=end-start, derivation=drv, result=result,
                          package_builds=builds)
            report["records"].append(record)
            (out / "results.json").write_text(json.dumps(report, indent=2) + "\n")
            print(label, trial, f"{end-start:.3f}s", len(builds), "package builds", flush=True)
    report["medians"] = {label: {phase: statistics.median(r[phase] for r in report["records"]
                            if r["label"] == label and not r["warmup"])
                            for phase in ["evaluation", "realization", "total"]} for label in roots}
    (out / "results.json").write_text(json.dumps(report, indent=2) + "\n")
    print(json.dumps(report["medians"], indent=2))


if __name__ == "__main__":
    main()
