#!/usr/bin/env python3
"""Compare export + Nix evaluation, excluding compilation and graph inspection."""

import argparse
import hashlib
import json
from pathlib import Path
import statistics
import subprocess
import tempfile
import time


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--before", type=Path, required=True)
    parser.add_argument("--after", type=Path, required=True)
    parser.add_argument("-C", "--source", type=Path, required=True)
    parser.add_argument("--package", default=".")
    parser.add_argument("--tags", default="")
    parser.add_argument("--ldflags", default="")
    parser.add_argument("--runs", type=int, default=3)
    parser.add_argument("--out", type=Path)
    args = parser.parse_args()
    if args.runs < 1:
        parser.error("--runs must be positive")
    out = args.out or Path(tempfile.mkdtemp(prefix="drvgo-benchmark-"))
    out.mkdir(parents=True, exist_ok=True)
    out = out.resolve()
    # A fresh directory prevents accidental replacement of earlier evidence.
    if any(out.iterdir()):
        parser.error("--out must be an empty directory")
    source = args.source.resolve()
    binaries = {"before": args.before.resolve(), "after": args.after.resolve()}
    report = {
        "scope": "export + evaluation only; no realization",
        "source": str(source),
        "package": args.package,
        "tags": args.tags,
        "ldflags": args.ldflags,
        "nix": subprocess.check_output(["nix", "--version"], text=True).strip(),
        "binaries": {label: {"path": str(path), "sha256": hashlib.sha256(path.read_bytes()).hexdigest()}
                     for label, path in binaries.items()},
        "records": [],
    }
    revision = subprocess.run(["git", "-C", str(source), "rev-parse", "HEAD"],
                              stdout=subprocess.PIPE, stderr=subprocess.DEVNULL, text=True)
    if revision.returncode == 0:
        report["source_revision"] = revision.stdout.strip()

    def run(label, trial, warmup=False):
        export = out / (label + "-export")
        command = [str(binaries[label]), "export", "-C", str(source),
                   "-out", str(export), "-tags", args.tags,
                   "-ldflags", args.ldflags, args.package]
        with (out / f"{label}-{trial}.log").open("w") as log:
            start = time.monotonic()
            subprocess.run(command, stdout=log, stderr=subprocess.STDOUT, check=True)
            exported = time.monotonic()
            result = subprocess.run(["nix-instantiate", str(export), "-A", "default"],
                                    stdout=subprocess.PIPE, stderr=log, text=True, check=True)
            end = time.monotonic()
        record = dict(builder=label, trial=trial, warmup=warmup, command=command,
                      export=exported-start, evaluation=end-exported, total=end-start,
                      derivation=result.stdout.strip())
        report["records"].append(record)
        (out / "results.json").write_text(json.dumps(report, indent=2) + "\n")
        print(f"{label} {trial}: {record['total']:.3f}s "
              f"(export {record['export']:.3f}s, evaluation {record['evaluation']:.3f}s)", flush=True)

    for label in binaries:
        run(label, "warmup", warmup=True)
    for trial in range(1, args.runs + 1):
        for label in (["before", "after"] if trial % 2 else ["after", "before"]):
            run(label, trial)
    report["medians"] = {
        label: {phase: statistics.median(r[phase] for r in report["records"]
                                        if r["builder"] == label and not r["warmup"])
                for phase in ["export", "evaluation", "total"]}
        for label in binaries
    }
    (out / "results.json").write_text(json.dumps(report, indent=2) + "\n")
    print(json.dumps(report["medians"], indent=2))
    print(f"Evidence: {out}")


if __name__ == "__main__":
    main()
