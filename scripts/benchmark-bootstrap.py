#!/usr/bin/env python3
"""Compare fresh drvgo CLI package builds, including unit tests and vet."""

import argparse
import hashlib
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
    parser.add_argument("--before", type=Path, required=True)
    parser.add_argument("--after", type=Path, required=True)
    parser.add_argument("--nixpkgs", type=Path, required=True, help="pinned nixpkgs source")
    parser.add_argument("--runs", type=int, default=7)
    parser.add_argument("--cores", type=int, default=2)
    parser.add_argument("--jobs", type=int, default=4)
    parser.add_argument("--out", type=Path)
    args = parser.parse_args()
    if min(args.runs, args.cores, args.jobs) < 1:
        parser.error("runs, cores, and jobs must be positive")
    roots = {label: getattr(args, label).resolve() for label in ("before", "after")}
    out = args.out.resolve() if args.out else Path(tempfile.mkdtemp(prefix="drvgo-bootstrap-"))
    out.mkdir(parents=True, exist_ok=True)
    if any(out.iterdir()):
        parser.error("output directory must be empty")
    flags = ["--cores", str(args.cores), "--max-jobs", str(args.jobs),
             "--option", "sandbox", "true", "--option", "sandbox-fallback", "false",
             "--option", "allow-import-from-derivation", "false",
             "--option", "substituters", "", "--option", "always-allow-substitutes", "false"]
    nonce = uuid.uuid4().hex[:12]
    report = {
        "scope": "Fresh CLI package builds with tests and vet, one warmup and alternating trials; "
                 "fresh per-build Go caches, warm SDK and filesystem caches, no substitutions. "
                 "This does not measure downstream planning, compilation, or network transfer.",
        "roots": {k: str(v) for k, v in roots.items()},
        "nixpkgs": str(args.nixpkgs.resolve()), "flags": flags, "nonce": nonce,
        "nix": subprocess.check_output(["nix", "--version"], text=True).strip(),
        "revisions": {k: subprocess.check_output(["git", "rev-parse", "HEAD"], cwd=v, text=True).strip()
                      for k, v in roots.items()},
        "records": [],
    }
    for trial in range(args.runs + 1):
        for label in (["before", "after"] if trial % 2 == 0 else ["after", "before"]):
            # A distinct derivation name forces a new build without deleting
            # store paths or changing the compiler or the source under test.
            name = f"drvgo-bootstrap-{nonce}-{label}-{trial}"
            expression = f"""
              let pkgs = import (builtins.toPath {nix_string(args.nixpkgs.resolve())}) {{}};
              in (import (builtins.toPath {nix_string(roots[label] / 'nix/package.nix')})
                {{ inherit pkgs; }}).overrideAttrs (_: {{ name = {nix_string(name)}; }})
            """
            expr = out / f"{label}-{trial}.nix"
            expr.write_text(expression)
            instantiate = ["nix-instantiate", str(expr), *flags]
            with (out / f"{label}-{trial}.log").open("w") as log:
                start = time.monotonic()
                drv = subprocess.check_output(instantiate, stderr=log, text=True).strip()
                evaluated = time.monotonic()
                realize = ["nix-store", "--realise", drv, *flags]
                output = subprocess.check_output(realize, stderr=log, text=True).strip()
                end = time.monotonic()
            record = {
                "label": label, "trial": trial, "warmup": trial == 0,
                "evaluation": evaluated - start, "realization": end - evaluated, "total": end - start,
                "instantiate": instantiate, "realize": realize, "expression": expression,
                "derivation": drv, "output": output,
                "binary_sha256": hashlib.sha256((Path(output) / "bin/drvgo").read_bytes()).hexdigest(),
                "nar_hash": subprocess.check_output(["nix-hash", "--type", "sha256", output], text=True).strip(),
                "references": sorted(subprocess.check_output(["nix-store", "--query", "--references", output], text=True).splitlines()),
            }
            report["records"].append(record)
            (out / "results.json").write_text(json.dumps(report, indent=2) + "\n")
            print(label, trial, f"{record['total']:.3f}s", record["nar_hash"], flush=True)
    report["medians"] = {
        label: {phase: statistics.median(r[phase] for r in report["records"]
                                        if r["label"] == label and not r["warmup"])
                for phase in ("evaluation", "realization", "total")}
        for label in roots
    }
    report["identical_outputs"] = all(
        len({json.dumps(r[field], sort_keys=True) for r in report["records"]}) == 1
        for field in ("binary_sha256", "nar_hash", "references")
    )
    (out / "results.json").write_text(json.dumps(report, indent=2) + "\n")
    print(json.dumps({k: report[k] for k in ("medians", "identical_outputs")}, indent=2))


if __name__ == "__main__":
    main()
