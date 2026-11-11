#!/usr/bin/env python3
"""Check dynamic evaluation, nested planning, and direct outputOf builds with iets."""

import argparse
import json
import os
from pathlib import Path
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
    parser.add_argument("--iets", type=Path, required=True, help="iets executable in a Nix store output")
    parser.add_argument("--runs", type=int, default=5)
    parser.add_argument("--out", type=Path)
    args = parser.parse_args()
    if args.runs < 1:
        parser.error("--runs must be positive")
    repo = Path(__file__).resolve().parent.parent
    binary = args.iets.resolve()
    package = binary.parent.parent
    if package.parent != Path("/nix/store"):
        parser.error("--iets must be a Nix-built package's bin/iets (needed in the sandbox)")
    out = (args.out or Path(tempfile.mkdtemp(prefix="drvgo-iets-"))).resolve()
    out.mkdir(parents=True, exist_ok=True)
    if any(out.iterdir()):
        parser.error("--out must be an empty directory")
    source = out / "source"
    shutil.copytree(repo / "tests/fixture", source)
    nonce = uuid.uuid4().hex[:12]
    env = dict(os.environ, IETS_NIX_FALLBACK="never", IETS_PROGRESS="false", NIX_REMOTE="daemon")
    flags = ["--option", "allow-import-from-derivation", "false"]
    features = ["--extra-experimental-features", "ca-derivations dynamic-derivations recursive-nix"]
    report = {"iets": str(binary), "nonce": nonce, "records": [], "checks": [],
              "scope": "Warm evaluation with tracing cache disabled; real daemon builds with IFD and classic fallback disabled. Nested planning uses iets too."}

    def run(command, label):
        start = time.monotonic()
        result = subprocess.run([str(x) for x in command], cwd=repo, env=env, text=True, capture_output=True)
        elapsed = time.monotonic() - start
        (out / (label + ".log")).write_text(result.stderr)
        if result.returncode:
            raise RuntimeError(f"{command}: see {out / (label + '.log')}\n{result.stderr}")
        return result.stdout.strip(), elapsed

    report["version"] = run([binary, "version"], "version")[0]
    report["nix_version"] = run(["nix", "--version"], "nix-version")[0]
    results = []
    for nested in [False, True]:
        label = "nested" if nested else "outer"
        expr = (f"import ./tests/iets.nix {{ iets = builtins.storePath {nix_string(package)}; "
                f"src = builtins.path {{ path = {nix_string(source)}; name = \"source\"; }}; "
                f"name = \"drvgo-iets-{nonce}-{label}\"; nested = {str(nested).lower()}; }}")
        commands = {
            "nix": ["nix-instantiate", *features, *flags, "--eval", "--strict", "--raw", "--read-write-mode", "-E", f"({expr}).drvPath"],
            "iets-1": [binary, "eval", "--impure", *flags, "--raw", "--no-tracing-cache", "--workers", "1", "-E", f"({expr}).drvPath"],
            "iets-4": [binary, "eval", "--impure", *flags, "--raw", "--no-tracing-cache", "--workers", "4", "-E", f"({expr}).drvPath"],
        }
        expected = None
        for trial in range(args.runs + 1):
            order = list(commands) if trial % 2 == 0 else list(reversed(commands))
            for client in order:
                value, elapsed = run(commands[client], f"{label}-{client}-{trial}")
                if expected is None:
                    expected = value
                assert value == expected, (label, client, value, expected)
                report["records"].append(dict(case=label, client=client, trial=trial, warmup=trial == 0, seconds=elapsed, derivation=value))
        build = [binary, "build", "--impure", *flags, "--no-tracing-cache", "--workers", "1", "-E", expr]
        result = run(build, label + "-build")[0]
        reference = run(["nix-build", *features, *flags, "--no-out-link", "-E", expr], label + "-reference")[0]
        assert result == reference, (result, reference)
        assert run(build, label + "-warm-build")[0] == result
        expected_stdout = "hello embedded world\nstable\nnested asset\n01 02 ff\nhidden asset"
        assert run([Path(result) / "bin/libnix-fixture"], label + "-run")[0] == expected_stdout
        results.append(Path(result).resolve())
        direct = f'builtins.outputOf ({expr}).planner.outPath "out"'
        direct_result = run([binary, "build", "--impure", *flags, "--no-tracing-cache", "-E", direct], label + "-direct")[0]
        direct_reference = run(["nix", "build", *features, *flags, "--impure", "--no-link", "--print-out-paths", "--expr", direct], label + "-direct-reference")[0]
        assert direct_result == direct_reference
        assert Path(direct_result).resolve() == results[-1]
        report["checks"].append(dict(case=label, derivation=expected, output=result, command_output=str(results[-1]), direct_output=direct_result))
        print(label, "evaluation/build/warm/direct PASS", flush=True)
    assert results[0] == results[1], "nested evaluator changed package derivations"
    (source / "message/assets/message.txt").write_text("iets changed embed\n")
    changed = run(build, "changed-build")[0]
    assert Path(changed).resolve() != results[-1]
    assert run([Path(changed) / "bin/libnix-fixture"], "changed-run")[0].startswith("iets changed embed\nstable\n")
    assert changed == run(["nix-build", *features, *flags, "--no-out-link", "-E", expr], "changed-reference")[0]
    report["embed_invalidation"] = "passed"
    runtime_outputs = []
    for nested in [False, True]:
        for explicit in [False, True]:
            label = f"runtime-nested-{nested}-explicit-{explicit}"
            fixture_expr = f"import ./tests/runtime-references.nix {{ explicit = {str(explicit).lower()}; }}"
            expr = (f"let f = {fixture_expr}; in import ./tests/iets.nix {{ "
                    f"iets = builtins.storePath {nix_string(package)}; inherit (f) src ldflags runtimeInputs; "
                    f'name = "drvgo-iets-{nonce}-{label}"; nested = {str(nested).lower()}; }}')
            nix_drv = run(["nix-instantiate", *features, *flags, "--eval", "--raw", "--read-write-mode", "-E", f"({expr}).drvPath"], label + "-nix-eval")[0]
            iets_drv = run([binary, "eval", "--impure", *flags, "--raw", "--no-tracing-cache", "-E", f"({expr}).drvPath"], label + "-iets-eval")[0]
            assert nix_drv == iets_drv
            result = run([binary, "build", "--impure", *flags, "--no-tracing-cache", "-E", expr], label + "-build")[0]
            assert result == run(["nix-build", *features, *flags, "--no-out-link", "-E", expr], label + "-reference")[0]
            assert run([Path(result)/"bin/runtime-reference"], label + "-run")[0] == "source data\nembedded data\nlinked data"
            data = json.loads(run(["nix-instantiate", "--eval", "--strict", "--read-write-mode", "--json", "-E",
                f"let f = {fixture_expr}; in {{ inherit (f) references unusedData; }}"], label + "-data")[0])
            closure = run(["nix-store", "--query", "--requisites", result], label + "-closure")[0].splitlines()
            assert set(data["references"]).issubset(closure), (data, closure)
            assert data["unusedData"] not in closure, (data, closure)
            direct = f'builtins.outputOf ({expr}).planner.outPath "out"'
            direct_result = run([binary, "build", "--impure", *flags, "--no-tracing-cache", "-E", direct], label + "-direct")[0]
            assert Path(direct_result).resolve() == Path(result).resolve()
            runtime_outputs.append(Path(result).resolve())
            report["checks"].append(dict(case=label, derivation=nix_drv, output=result, command_output=str(runtime_outputs[-1])))
            print(label, "evaluation/build/direct/closure PASS", flush=True)
    assert len(set(runtime_outputs)) == 1, "runtime input declaration or nested evaluator changed package outputs"
    report["medians"] = {
        case: {client: statistics.median(r["seconds"] for r in report["records"]
                                        if r["case"] == case and r["client"] == client and not r["warmup"])
               for client in commands}
        for case in ["outer", "nested"]
    }
    (out / "results.json").write_text(json.dumps(report, indent=2) + "\n")
    print(json.dumps(report["medians"], indent=2))
    print(f"Evidence: {out}")


if __name__ == "__main__":
    main()
