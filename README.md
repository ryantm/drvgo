# drvgo

**Package-granular Go builds with Nix.**

An experimental Go build backend inspired by [cargo (libnix)](https://github.com/nixcloud/cargo/tree/rust-1.99.0-libnix) and its [Rust 1.99 release](https://lastlog.de/blog/libnix_rust_1.99_release.html). Go resolves the build; Nix builds and caches individual packages, including the standard library, in its sandbox.

This is a standalone Go CLI, not a fork of `cmd/go` or bindings to Nix's C++ library. It exports Nix expressions and invokes Nix's CLI. The current implementation supports **build, run, and standalone export for one main package**. It is not complete Cargo-backend feature parity.

## Use

Prerequisites: Linux, Nix with `nix-command` and `flakes` enabled, and access to a Nix store that can build derivations. The tested platform is x86_64-linux. A pinned Go 1.26.5/C toolchain is built or downloaded automatically; it does not install anything system-wide.

```sh
nix-shell --run 'go build -o bin/drvgo ./cmd/drvgo'

bin/drvgo build -C /path/to/project ./cmd/example
/path/to/project/result/bin/example

bin/drvgo run -C /path/to/project ./cmd/example -- --help

# Export source snapshots and the package graph without building it.
bin/drvgo export -C /path/to/project -out nix-build ./cmd/example
nix-build /path/to/project/nix-build -A default
```

Flags must precede the package argument. `-tags` and `-ldflags` pass through to Go. `-out` selects the export directory (default `.drvgo`); `-o` selects the Nix result symlink (default `result`). Both are relative to `-C`. The result is a directory with `bin/<command>`, not a single executable file.

The generated directory contains `default.nix`, `toolchain.nix`, `toolchain-builder.nix`, `builder.sh`, `scripts.nix`, `graph.json`, `evaluation.json`, `resolved-toolchain.nix`, and `sources/`. It can be moved or checked in and built without the original checkout, Go module cache, or this CLI. Its toolchain pin supports fetching the same tools on another machine. Nix's result symlink roots the built output against garbage collection.

The project also includes `devenv.nix`/`devenv.yaml`. The tested `nix-shell` entry point and generated builds use the same immutable nixpkgs revision. No directory trust or shell hooks are changed automatically.

## Consume as a Nix flake

The flake provides `packages.<system>.drvgo` (also `default`),
`apps.<system>.drvgo` (also `default`), and `lib.mkGoPackage` for
`x86_64-linux` and `aarch64-linux`.

```sh
nix run github:ryantm/drvgo/v0.1.0 -- export -C /path/to/project .
nix profile install github:ryantm/drvgo/v0.1.0
```

Add drvgo as an input to build your project directly:

```nix
{
  inputs.drvgo.url = "github:ryantm/drvgo/v0.1.0";

  outputs = { self, drvgo }: {
    packages.x86_64-linux.default = drvgo.lib.mkGoPackage {
      system = "x86_64-linux";
      src = self;
      package = "./cmd/my-app";
      name = "my-app";
      # null for vendored projects or projects without external modules.
      # Otherwise supply the module-proxy hash described below.
      vendorHash = null;
    };
  };
}
```

The CLI package builds with ordinary Nix flakes enabled. Calling `mkGoPackage`
uses dynamic derivations and requires the client/daemon features documented in
[Source-only dynamic builds](#source-only-nix-builds-with-dynamic-derivations). It accepts the same
arguments as `nix/dynamic.nix`, plus an explicit native `system`. Keep drvgo's
nixpkgs input pinned: its Go dry-run parser requires Go 1.26.5.

Releases are published to [FlakeHub](https://flakehub.com/flake/ryantm/drvgo)
and [Flakestry](https://flakestry.dev/flake/ryantm/drvgo). For FlakeHub, replace the
input URL with `https://flakehub.com/f/ryantm/drvgo/0.1.0.tar.gz`.
The GitHub Actions workflow checks the flake before publishing `v*.*.*` tags to
both registries using OIDC; no registry secrets are required.

## Embedded files

`//go:embed` is supported for strings, byte slices, and `embed.FS`, including nested directories, glob patterns, and `all:` hidden files. The exporter uses `go list -deps -json`'s resolved `EmbedFiles`; it does not approximate Go's pattern rules. It copies the files into the embedding package's snapshot and rewrites the compiler's `embedcfg` to refer to that snapshot.

Changing an embedded file changes the embedding package's derivation and its dependents. Unrelated packages remain cached. Missing or invalid embeds are rejected by Go during planning. Generate required assets before exporting; `go generate` is not run implicitly.

## How it works

1. Build the pinned toolchain, including precompiled `buildid` and `pack` helpers.
2. Ask that toolchain for package metadata and `go build -n -a -work -p 1 -trimpath -buildvcs=false` commands. Go handles module selection, replacements, build tags, compiler flags, assembly, and cgo.
3. Split the trace into package actions, resolve relative source paths, translate Go argument quoting, and identify dependencies through temporary archive references.
4. Snapshot selected package files. Native packages additionally preserve module-relative auxiliary sources for parent-directory C includes.
5. Emit one Nix derivation per package. Dependency archive references become Nix output paths; each package compiles once. The command package also links its executable. Scripts are passed as files to avoid large environment-argument limits.
6. Build with Nix. The sandbox has no source checkout or module cache and needs no module-download network access. Planning may download modules through Go's usual checksum-verifying machinery.

The dry-run format is **not a stable Go API**, so the driver is deliberately pinned to Go 1.26.5. The translator handles embedded JSON heredocs, Go-style argument quoting, and cgo's dynamic-import fallback. The latter packs Go's `dynimportfail` marker when preliminary native linking fails, allowing the final link to resolve symbols across packages.

## Verification

```sh
go test ./...
go vet ./...
# Requires Nix; performs real isolated builds and cache-invalidation checks.
go test -tags integration -v ./internal/export

# Pinned real-world build, runtime database, and smoke tests.
examples/upm/test.sh
```

See [the UPM example](examples/upm/README.md) for the tested revision and results.

## Current boundaries

- One native Linux main package per invocation. amd64 was tested; arm64 is accepted but untested. Cross-compilation, Darwin, race/coverage modes, test binaries, shared-library modes, and install commands are not implemented.
- Requires the pinned compiler. Changing compiler versions needs translator regression testing, not just a version-string edit.
- cgo uses the pinned GCC toolchain. System libraries and `pkg-config` dependency injection are not implemented. SWIG and Fortran are unsupported. Native sources that require undeclared files outside their module cannot build.
- cgo snapshots include auxiliary module files, so native-package invalidation is coarser than pure-Go package invalidation. Arbitrary native build generators are not run automatically.
- Project and module-cache directory names containing shell-special characters or spaces are rejected. Embedded asset names are recorded as JSON and do not have this directory-name restriction. Symlink source files are rejected.
- Each export copies sources locally, then Nix imports content-addressed source snapshots. Registry fetch derivations and direct libnix bindings are future work.
- Re-exporting rereads package metadata and sources even for a fully cached build. Nix build overhead is greater than Go's ordinary local cache; the benefit is independently reusable Nix outputs and a standalone build graph.

Upstream reference inspected: `nixcloud/cargo` commit `1e7173f` on `rust-1.99.0-libnix`, especially `src/compiler/nix_build`, its templates, and the runner. This implementation is new Go code, not a translation of Cargo's Rust source.

## Source-only Nix builds with dynamic derivations

`nix/dynamic.nix` runs the planner during the build and uses
`builtins.outputOf` to build its generated derivation. No generated export needs
to be committed, and evaluation works with `allow-import-from-derivation = false`.

```nix
let buildGo = import ./nix/dynamic.nix {};
in buildGo {
  name = "my-command";
  src = ./my-project;
  package = "./cmd/my-command";
  vendorHash = "sha256-...";
}
```

`vendorHash` hashes the module proxy produced by `buildGoModule` with
`proxyVendor = true`. Use `pkgs.lib.fakeHash` initially and replace it with the
reported hash. Use `null` only for projects requiring no downloaded modules.
The module download is a fixed-output derivation; the planner uses that proxy
offline. `tags` and `ldflags` are optional strings. Source trees should exclude
build outputs and unrelated files to avoid unnecessary replanning.

Both the client and daemon need these experimental features:

```nix
nix.settings.experimental-features = [
  "nix-command" "flakes" "ca-derivations" "dynamic-derivations" "recursive-nix"
];
nix.settings.system-features = [ "recursive-nix" "ca-derivations" ];
```

Distributed builders must also advertise `recursive-nix` and `ca-derivations` in their
`supportedFeatures`. The sandbox remains enabled. The planner prefers local
execution: current remote build hooks can return its generated `.drv` without
copying the new dependency derivations. On machines configured with `max-jobs = 0`,
pass `--max-jobs 1` to permit local planning; generated package builds remain
eligible for remote builders. Fully remote planning is not supported by this workflow. The planner's nested evaluator
instantiates the graph without importing any derivation output; Nix schedules
the resulting package builds. Package derivations retain the same toolchain
context as ordinary exports, allowing them to share cached builds.

```sh
nix-build examples/upm/dynamic.nix -A package \
  --option allow-import-from-derivation false
make dynamic
```

The UPM example builds the command, database generator, database, and wrapper
from a pinned upstream source revision. The dynamic integration test checks
embedded assets, cross-package cgo linking, unchanged output reuse, and that
an embedded-file edit invalidates exactly the embedding package and command.
The existing `export`, `build`, and `run` CLI workflows remain available for
Nix installations without these experimental features.

## Planning performance and CPU budgets

The exporter indexes source directories once and rewrites each command script
in one pass, instead of scanning every script once per source directory. It
requests only the `go list` fields it uses, avoiding redundant stale-build
analysis, and accumulates dry-run scripts without repeatedly copying them.
Go still discovers packages and selected files on every export: there is no
persistent metadata cache that could miss new source files, imports, build tags,
workspace changes, or newly matched embedded assets.

Package builders set both `GOMAXPROCS` and the compiler's `-c` flag from
`NIX_BUILD_CORES`. `--cores 0` uses the available CPUs; a missing budget defaults
to one. Host-side planning uses Go's normal CPU limit, or the caller's
`GOMAXPROCS`, without embedding that limit in package recipes. `drvgo build`
currently requests four Nix jobs and two cores per job. Standalone exports and
dynamic builds accept the usual Nix scheduling flags.

The scheduling correction changes existing recipes, requiring a one-time
rebuild of older package outputs. Subsequent changes to the Nix core budget or
the planner's CPU limit do not change the derivation identities. It does not
strip debug information or otherwise change binary postprocessing.

Measured on replit1 (Linux amd64, Intel Core i7-1280P), October 22, 2026,
against drvgo commit `6b18ee1`, using Go 1.26.5 and Determinate Nix 3.24.0:

| Target | Export before / after | Export + evaluation before / after |
| --- | --- | --- |
| Kubernetes v1.37.1 API server | 10.79 / 1.87 s | 13.54 / 4.41 s |
| Mattermost v11.9.0 server | 3.49 / 1.34 s | 4.90 / 2.79 s |

These are medians of three warm runs, with alternating builder order. Compilation
and realization are excluded; this is not a Bookie/go2nix comparison or a cold-build
benchmark. Per-phase medians need not sum to the total median. The planning-only
optimization preserved both projects' complete exported graphs and derivation
identities before the separate CPU-budget correction. Unit tests, `go vet`, and
source-selection/cache-invalidation integration tests passed. Source-only dynamic
builds of the fixture and Kubernetes passed with IFD disabled. The Kubernetes
build used four jobs and two cores per job and passed version/help smoke tests;
no cluster was started. See [raw timings and revisions](benchmarks/planning-2026-10-22.json).

A second optimization round moves dependency interpolation into generated
`scripts.nix`, records freshly computed NAR hashes for each package snapshot,
and collects independent snapshots with up to four workers (bounded by the
planner's `GOMAXPROCS`). Nix can use those hashes to reuse source store paths
without reimporting every directory. Sources are still rediscovered, copied,
and hashed on every export; no filesystem metadata cache is trusted.

Compared with the first-round optimized executable, under the same warm,
alternating three-run methodology:

| Target | Evaluation before / after | Export + evaluation before / after | Further speedup |
| --- | --- | --- | --- |
| Kubernetes v1.37.1 API server | 2.76 / 0.82 s | 4.71 / 2.74 s | 1.72× |
| Mattermost v11.9.0 server | 1.48 / 0.56 s | 2.83 / 1.87 s | 1.51× |

This did not reach the additional 4× overall target. Fresh Go discovery and
snapshot export now dominate. All package derivation identities were preserved
in both projects, so this round does not require recompilation of their cached
outputs. See [second-round timings and binary hashes](benchmarks/planning-round2-2026-10-22.json).
Tests compare NAR hashes with `nix-hash`, compare generated Nix strings and
contexts with the previous interpolation, and check Go edits, new embeds,
test-only changes, standalone exports, and concurrent snapshot collection.
Unit tests, `go vet`, integration tests, and race detection passed. The fixture
and Kubernetes source-only dynamic builds passed with IFD disabled; Kubernetes
reused its previous output and passed version/help smoke tests.

Export directories are generated, content-pinned artifacts. Change the original
project and rerun `drvgo export`; manually editing `sources/` without regenerating
its recorded hashes can reuse the previously pinned store contents or fail hash
verification. Keep the entire generated directory together when moving an export to another
machine.

The third round runs `go list` and dry-run planning concurrently, cancelling and
joining the sibling process on failure. `evaluation.json` contains only the
metadata Nix consumes, while `graph.json` retains the full diagnostic graph.
`resolved-toolchain.nix` reuses the already resolved toolchain with its original
derivation output context. If its output or derivation is unavailable, evaluation
falls back to the pinned `toolchain.nix`. An explicit Nix `toolchain` argument
still takes precedence. The existing CLI `-toolchain` option additionally avoids
the initial toolchain resolution during export; dynamic builds already use it.

On October 23, three alternating warm trials against `c5b2418` measured:

| Target | Export before / after | Evaluation before / after | Total before / after |
| --- | --- | --- | --- |
| Kubernetes API server | 1.27 / 1.19 s | 0.50 / 0.34 s | 1.79 / 1.52 s |
| Mattermost server | 0.87 / 0.83 s | 0.35 / 0.19 s | 1.22 / 1.02 s |

This is approximately 15–16% less export-plus-evaluation time. Compare within
this session: absolute timings differ from earlier rounds. Compilation is
excluded. All 2,079 Kubernetes and 1,086 Mattermost package derivations were
unchanged. Tests cover the toolchain fallback, concurrent process startup and
cancellation, embed invalidation, and race detection. Dynamic fixture and
Kubernetes builds passed with IFD disabled; Kubernetes reused the same output
and passed version/help smoke tests.
See [third-round measurements](benchmarks/planning-round3-2026-10-23.json).

To measure export plus Nix evaluation against another drvgo executable:

```sh
python3 scripts/benchmark-export.py \
  --before /path/to/old/drvgo --after ./bin/drvgo \
  -C /path/to/project --package ./cmd/example --runs 3
```

The script warms both exporters, alternates their order, and saves commands,
logs, individual timings, derivation paths, and medians in a temporary directory.
It excludes compilation, realization, and measurement-only graph inspection.
For CPU profiling of the exporter itself:

```sh
DRVGO_BENCH_DIR=/path/to/project DRVGO_BENCH_PACKAGE=./cmd/example \
  go test ./internal/export -run '^$' -bench '^BenchmarkGenerate$' \
  -benchtime=1x -cpuprofile=/tmp/drvgo.cpu -o /tmp/drvgo-export.test
go tool pprof /tmp/drvgo-export.test /tmp/drvgo.cpu
```

## License

Dedicated to the public domain under [CC0 1.0 Universal](LICENSE).
Dependencies and the upstream projects used by the examples retain their own licenses.
