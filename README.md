# drvgo

**Package-granular Go builds with Nix.**

An experimental Go build backend inspired by [cargo (libnix)](https://github.com/nixcloud/cargo/tree/rust-1.99.0-libnix) and its [Rust 1.99 release](https://lastlog.de/blog/libnix_rust_1.99_release.html). Go resolves the build; Nix builds and caches individual packages, including the standard library, in its sandbox.

This is a standalone Go CLI, not a fork of `cmd/go` or bindings to Nix's C++ library. It exports Nix expressions and invokes Nix's CLI. The current implementation supports **build, run, and standalone export for one main package**. It is not complete Cargo-backend feature parity.

## Use

Prerequisites: Linux (amd64 or arm64) or macOS on Apple Silicon (M series), Nix with `nix-command` and `flakes` enabled, and access to a Nix store that can build derivations. A pinned Go 1.26.5/C toolchain is built or downloaded automatically; it does not install anything system-wide.

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

The generated directory contains `default.nix`, `toolchain.nix`, `toolchain-builder.nix`, `runner.go`, `buildid.go`, `pack.go`, `pack.patch`, `scripts.nix`, `graph.json`, `evaluation.json`, `resolved-toolchain.nix`, and `sources/`. It can be moved or checked in and built without the original checkout, Go module cache, or this CLI. Its toolchain pin supports fetching the same tools on another machine. Nix's result symlink roots the built output against garbage collection.

The project also includes `devenv.nix`/`devenv.yaml`. The tested `nix-shell` entry point and generated builds use the same immutable nixpkgs revision. No directory trust or shell hooks are changed automatically.

## Test binaries and build modes

`drvgo export -test ./pkg/example` exports one package's test executable,
using cmd/go's generated test main. Run the resulting `bin/<import-path-tail>.test`
from the package's source directory so relative fixtures remain available.
Internal tests, external tests, custom `TestMain`, and test embeds are included. This compiles
tests; it does not run them or silently replace cmd/go's test harness.
Select a package with tests; packages without tests are rejected.

```sh
drvgo export -test -cgo 0 -out /tmp/example-tests ./pkg/example
tests=$(nix-build /tmp/example-tests -A default --no-out-link)
(cd pkg/example && "$tests/bin/example.test" -test.v)
```

`-race` enables the race detector for commands or test binaries. `-cgo 0` and
`-cgo 1` explicitly select cgo; it remains enabled by default. Race builds require
cgo, so `-race -cgo 0` is rejected. The flake helper accepts `test`, `race`, and
`cgoEnabled` (a boolean, or `null` for the default) respectively.
Its dynamic planner still performs discovery at build time with IFD disabled.
Coverage and multi-package test runners are not implemented.

## Consume as a Nix flake

The flake provides `packages.<system>.drvgo` (also `default`),
`apps.<system>.drvgo` (also `default`), and `lib.mkGoPackage` for
`x86_64-linux`, `aarch64-linux`, and `aarch64-darwin`.

```sh
nix run github:ryantm/drvgo -- export -C /path/to/project .
nix profile install github:ryantm/drvgo
```

Apple Silicon support requires a revision newer than v0.1.0. The commands above
use the current default branch; a flake lockfile pins the selected revision.

Add drvgo as an input to build your project directly:

```nix
{
  inputs.drvgo.url = "github:ryantm/drvgo";

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

For Apple Silicon, use `aarch64-darwin` in both system positions above.

The CLI package builds with ordinary Nix flakes enabled. Calling `mkGoPackage`
uses dynamic derivations and requires the client/daemon features documented in
[Source-only dynamic builds](#source-only-nix-builds-with-dynamic-derivations). It accepts the same
arguments as `nix/dynamic.nix`, plus an explicit native `system`. Select a tested Go version as described below; keep the compiler itself pinned.

Releases are published to [FlakeHub](https://flakehub.com/flake/ryantm/drvgo)
and [Flakestry](https://flakestry.dev/flake/github/ryantm/drvgo). For FlakeHub, replace the
input URL with `https://flakehub.com/f/ryantm/drvgo/0.1.0.tar.gz`.
The GitHub Actions workflow checks the flake before publishing `v*.*.*` tags to
both registries using OIDC; no registry secrets are required.

## Go version compatibility

The compiler used to build the drvgo CLI is separate from the compiler used for
its target. Each target toolchain builds `buildid` and `pack` with that same Go
version. Tested versions are an explicit allowlist, not a promise that all patch
releases or future minors have identical dry-run formats.

| Target Go | Flake toolchain output | Adapter | Local validation |
| --- | --- | --- | --- |
| 1.25.12 | `toolchain-go1_25` | `cmd-go-1.25` | Linux amd64 |
| 1.26.5 (default) | `toolchain-go1_26` | `cmd-go-1.26` | Linux amd64 |
| 1.26.6 | `toolchain-go1_26_6` | `cmd-go-1.26` | Linux amd64 |

These outputs and the selectable `go` argument are newer than v0.1.0. From a
checkout containing this change:

```sh
tc=$(nix build .#toolchain-go1_25 --no-link --print-out-paths)
./bin/drvgo export -C /path/to/project -toolchain "$tc" .
```

For a flake consumer, pass a Go derivation to `mkGoPackage`, for example:

```nix
drvgo.lib.mkGoPackage {
  system = "x86_64-linux";
  go = drvgo.packages.x86_64-linux.toolchain-go1_25.go;
  src = self;
  package = "./cmd/my-app";
  vendorHash = null;
}
```

`drvgo.lib.mkToolchain { system = "x86_64-linux"; go = pkgs.go_1_25; }`
also constructs a bundle directly. Any Go derivation may be supplied, but its
reported version must pass the tested-version policy. Use CLI `-experimental-go`
or `mkGoPackage { experimentalGo = true; ... }` to try an untested numbered
release >= 1.23, including prereleases. The graph records that it is experimental
and the CLI reports it; parser checks remain enabled. Development snapshots with
unparseable version strings are rejected.

Go commands run with `GOTOOLCHAIN=local`. drvgo checks the main module's and
active workspace's `go` minimums and explains when the selected compiler is too
old. A newer `toolchain` suggestion is reported without silently downloading or
switching compilers. The workspace's suggestion takes precedence over the
module's; cmd/go still validates full module/workspace syntax and dependency
requirements. Keep project files stable while exporting.

Changing compilers produces different package derivations. A standalone export
using a custom compiler needs that toolchain's output and derivation on the
receiving machine, or an explicit `toolchain` argument supplied when importing
it. If they are unavailable, the exporter will not substitute Go 1.26.5. Flake
consumers should select their compiler declaratively through `mkGoPackage`.

The compatibility suite compares normal `go build` runtime output against drvgo
for cgo with cross-package native symbols, embedded files, assembly, build tags,
linker flags, workspaces, and offline vendoring. It also checks unchanged-build
reuse, Go edits, test-only/unrelated files, new/changed embeds, standalone exports,
and dynamic builds with IFD disabled. Test-binary checks cover internal/external
tests, both sets of embeds, custom `TestMain`, runtime fixtures, relocated source
trees, application-main isolation, and intentional failure propagation. The
matrix also compares cgo-disabled and race-enabled commands against `go build`,
requires a race-enabled test binary to detect an intentional data race, and
checks that dynamic test builds share their outputs with standalone exports.
Recorded compressed traces and expected
normalized-plan hashes protect each adapter against regressions. In particular,
Go 1.25 prints action headers before `mkdir`, unlike Go 1.26; the adapter
normalizes that boundary before using the shared parser.

To reproduce one matrix row:

```sh
nix-shell --run 'make build; go test -c -tags integration -o /tmp/drvgo-integration.test ./internal/export'
tc=$(nix build .#toolchain-go1_25 --no-link --print-out-paths)
python3 tests/compatibility.py --toolchain "$tc" --drvgo bin/drvgo \
  --integration-test /tmp/drvgo-integration.test --flake-toolchain toolchain-go1_25
```

The dynamic portion requires the features documented below. CI is configured for
all three versions on Linux amd64 and arm64 and Apple Silicon macOS (`macos-15`),
and gates future registry releases on those jobs. macOS builds use Nix’s Clang
and pinned Apple SDK. The matrix also exercises C++ compilation and, on macOS,
SDK framework linking and native arm64 Mach-O output. [Local results](tests/compatibility-results-2026-10-23.json) include
15 cross-version case comparisons with no shared package derivations. To add a release,
pin its compiler, add an adapter/policy entry, run the matrix with
`--record-traces`, review the generated trace, then deliberately regenerate the
expected plan with `DRVGO_UPDATE_TRACES=1 go test ./internal/plan -run TestRecordedDrivers`.

### Toward an upstream build-plan interface

The long-term replacement for dry-run parsing would be a versioned, machine-readable
cmd/go planning API. This is a design target, not an API drvgo currently relies on
or an upstream proposal that has been submitted. It would need:

- Stable action IDs, dependency edges, and exact compiler/toolchain identity.
- Selected source files, resolved embeds, module/workspace inputs, and build settings.
- Structured argv/environment/working-directory fields and generated file contents.
- Explicit conditional operations for cases such as cgo's failed preliminary link.
- Versioned capability negotiation and deterministic plans that can be replayed offline.

An adapter would translate this representation into drvgo's existing package
graph, alongside the trace adapters. `go build -json` currently reports build
output and failures; it is not that complete replayable interface. This direction
should preserve cmd/go's source selection rather than reimplementing it in drvgo.

## Optional empty test results

Test exports remain strict by default. For filesystem-discovered candidate
directories, use `mkGoPackage { test = true; allowEmptyTests = true; ... }`
or `drvgo export -test -allow-empty-tests`. Exactly one package is required.
The selected Go compiler determines platform, release, cgo and build-tag
eligibility. Successful `go list` results avoid compiling a selection helper;
excluded directories still use that compiler's typed `go/build` errors.
No-test packages with eligible ordinary sources
are actually compiled during planning, so load, syntax and type errors remain
fatal. Entirely excluded source directories may produce an empty result.

An empty result has no `bin` directory and contains
`nix-support/no-tests` with `{"version":1,"status":"no-tests"}` and a trailing
newline. Consumers must skip only this explicit marker, never infer a skip
from a missing binary. Eligible internal or external tests follow the normal
test-binary path. This opt-in does not permit multiple packages or convert
failed tests/builds into skips.

## Embedded files

`//go:embed` is supported for strings, byte slices, and `embed.FS`, including nested directories, glob patterns, and `all:` hidden files. The exporter uses `go list -deps -json`'s resolved `EmbedFiles`; it does not approximate Go's pattern rules. It copies the files into the embedding package's snapshot and rewrites the compiler's `embedcfg` to refer to that snapshot.

Changing an embedded file changes the embedding package's derivation and its dependents. Unrelated packages remain cached. Missing or invalid embeds are rejected by Go during planning. Generate required assets before exporting; `go generate` is not run implicitly.

## How it works

1. Build the pinned toolchain, including the native replay runner with Go's build-ID and archive implementations linked in.
2. Ask that toolchain for package metadata and `go build -n -a -work -p 1 -trimpath -buildvcs=false` commands. Go handles module selection, replacements, build tags, compiler flags, assembly, and cgo.
3. Split the trace into package actions, resolve relative source paths, translate Go argument quoting, and identify dependencies through temporary archive references.
4. Snapshot selected package files. Native packages additionally preserve module-relative auxiliary sources for parent-directory C includes.
5. Emit one Nix derivation per package. Dependency archive references become Nix output paths; each package compiles once. The command package also links its executable. Scripts are passed as files to avoid large environment-argument limits.
6. Build with Nix. The sandbox has no source checkout or module cache and needs no module-download network access. Planning may download modules through Go's usual checksum-verifying machinery.

The dry-run format is **not a stable Go API**. drvgo selects a tested version-specific adapter; Go 1.26.5 remains the default. The translator handles embedded JSON heredocs, Go-style argument quoting, and cgo's dynamic-import fallback. The latter packs Go's `dynimportfail` marker when preliminary native linking fails, allowing the final link to resolve symbols across packages.

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

- One native main package or test binary per invocation on Linux amd64/arm64 or Apple Silicon macOS. Cross-compilation, Intel macOS, coverage modes, shared-library modes, and install commands are not implemented.
- Requires a tested compiler or an explicit experimental override. New versions need translator and differential-build testing, not just a version-string edit.
- cgo uses pinned GCC on Linux and Clang with the pinned Apple SDK on macOS. Apple SDK frameworks are available; additional system libraries and `pkg-config` dependency injection are not implemented. SWIG and Fortran are unsupported. Native sources that require undeclared files outside their module cannot build.
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

Store paths embedded verbatim in selected source files, `go:embed` assets, or
linker flags are retained as runtime dependencies when they are valid in the
planner's store. Nix string context normally supplies these inputs through
`src` or `ldflags`. For paths written literally in an existing checkout, declare
them with `runtimeInputs = [ runtimeData ];` so the recursive planner can see
them. This also works with `lib.mkGoPackage`. Only references used by selected
packages enter their build inputs; unused `runtimeInputs` and excluded source
files do not enlarge the resulting runtime closure. Encoded or dynamically
constructed paths require an explicit reference in the selected source or flags.

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

### CI toolchain and output sizes

The [November 11 measurements](benchmarks/ci-2026-11-11.md) show a 33.7% faster
full rebuild of a 1,061-unit graph and an 85.5% smaller compressed compile
toolchain closure (81.5 MiB to 11.8 MiB). Cached dynamic planning remains roughly
flat in that round. The report includes the planning-closure tradeoff, iets PR 3 comparisons,
raw samples, and the Linux/Apple Silicon compatibility results.

The [next CI planning round](benchmarks/ci-round2-2026-11-11.md) reduces fresh
planning of the same cached 1,061-unit graph from 3.77 s to 2.42 s (35.7%) with
remote cache queries excluded. Small snapshots, up to 4 KiB including filename
and metadata overhead, are encoded in the existing file-backed replay script.
The native runner restores their exact bytes and executable modes in its build
directory. Larger snapshots stay separate unless they fit the compression limits
below. This avoids a store
registration per small snapshot while preserving package-level invalidation.

Pure Go and Go assembly package actions receive only the compiler, assembler,
and native replay runner. Selected standard-library sources and
quoted native headers are snapshotted per package, so workers no longer need the
full Go SDK. Pure Go links add the linker; cgo, race, native objects, and explicit
external-link flags select the native compiler bundle. Planning uses the full
SDK and native tools by default. With `cgoEnabled = false`, dynamic planning
needs only the SDK plus worker derivation recipes; the generated graph retains
the original worker dependencies. All bundles use the same pinned Go release.

In the [third CI optimization round](benchmarks/ci-round3-2026-11-11.md), this
reduced the pure-Go planner's compressed input closure from 235 to 118 MiB.
Avoiding unnecessary helper compilation reduced fresh dynamic builds of an
optional no-test package from 3.29 to 0.62 seconds and tagged tests from 3.79 to
1.14 seconds, with tools and package outputs cached and remote queries disabled.

The [fourth round](benchmarks/ci-round4-2026-11-11.md) integrates Go's archive
tool into the native worker, reducing the compressed compile-toolchain closure
another 7.3%, from 11.84 to 10.98 MiB. Archive replay and helper builds are faster;
the full 62-package fixture rebuild remains essentially unchanged.

The [compressed snapshot round](benchmarks/ci-round5-2026-11-11.md) reduces fresh
UPM planning from 3.81 to 3.43 seconds (10.0%). Snapshots up to 64 KiB decoded can
now travel compressed when their payload, filenames, and metadata fit a 16 KiB
budget before base64 encoding. The original raw path remains for snapshots up
to 4 KiB. This removes 273 store objects from UPM's graph while adding 0.30 MiB
to its compressed cache payload; package rebuild time stays essentially flat.

The runner performs file operations directly and calls that Go release's build-ID
and archive implementations in process, including Mach-O signing support.
Archive handling uses the selected SDK's `cmd/pack` source with a small entry-point
patch; it preserves Go's archive format and avoids a separate executable and
process per append. Compiler, assembler,
linker, and native compilation still use the pinned tools. Source snapshots are
copied and NAR-hashed in one pass. Ordinary exports and dynamic builds preserve
derivation contexts. They share package outputs when Go discovery emits identical
action IDs; relative cgo include paths can currently prevent that sharing. Custom toolchains without the
separate `compile`, `link`, or `native` attributes use the supplied full bundle;
they must include the current native runner.

The dynamic planner uses explicit tools without stdenv setup. It and its two
forwarding outputs build locally with `allowSubstitutes = false`; forwarding
only creates symlinks. Go package outputs remain substitutable and eligible for
remote builders. A planner cache hit would copy its generated derivation/source
closure, so the planner instead recomputes that graph locally.
If your cache publishes planner outputs and CI often rebuilds an identical
source tree on a cold worker, set `allowPlannerSubstitutes = true` in
`mkGoPackage` to allow those hits to avoid downloading the planning toolchain.

Nix's `always-allow-substitutes = true` overrides these hints. For CI, leave this
setting at its default `false`, or pass `--option always-allow-substitutes false`
to the build command. Otherwise cache metadata misses can dominate planning
time, even when every compiled package is already available locally.

Library outputs contain their `_pkg_.a` archive. Command outputs contain only
`bin/<name>`; the main package archive is no longer copied alongside the binary.
This avoids storing embedded command assets twice. Builders move completed files
into their outputs instead of copying them. Debug information is preserved.
These recipe changes cause a one-time rebuild of existing package outputs.

Measured on Linux amd64 with Go 1.26.5 on November 10, 2026:

| Measurement | Before | After |
| --- | ---: | ---: |
| Pure Go builder toolchain closure, NAR bytes | 614.4 MiB | 292.2 MiB |
| Same closure compressed with zstd level 3 | 182.7 MiB | 81.5 MiB |
| Command with an 8 MiB embedded asset, output NAR | 18.3 MiB | 10.3 MiB |
| Same command, compressed cache payload | 17.4 MiB | 9.4 MiB |

The compressed toolchain payload is 55% smaller. These are complete closures
from an empty file binary cache, excluding metadata and protocol overhead;
they are byte counts, not network transfer timings. A worker that also builds
cgo packages or links commands still needs the native toolchain (618.0 MiB).
The embedded-asset example uses deterministic incompressible data; savings for
other commands depend on their assets and main package size.
Compilation and ordinary Nix evaluation were essentially unchanged: rebuilding
the fixture's 67 outputs with `--check` took 4.30 / 4.26 s before / after, and
exporting plus evaluating the 1,061-unit synthetic graph took 0.670 / 0.674 s.
These are warm measurements; `--check` has dependencies already available and
does not model a cold store. The main gain is fewer bytes to transfer.

To compare dynamic CI builds from two drvgo checkouts:

```sh
python3 scripts/benchmark-ci.py \
  --before /path/to/baseline-drvgo --after . \
  --source tests/fixture --runs 5
```

The benchmark warms tools and package outputs, alternates checkout order, and
forces a fresh planner on every trial with a unique forwarding name. It measures
outer evaluation separately from realization (planning plus forwarding), checks
that package outputs remain identical across trials, and saves logs and timings.
IFD stays disabled. Sources must need no downloaded modules, or supply a vendor
tree. Cache configuration and latency affect the realization measurements.
Use `--nix-option always-allow-substitutes false` to honor derivation hints.
To isolate local planning costs with already available inputs, also pass
`--nix-option substituters ''`; this disables remote lookups for that benchmark
command only. Apply identical settings to both revisions.

### iets dynamic-derivation compatibility

Tested with [replit/iets PR #3](https://github.com/replit/iets/pull/3), commit
`69883a64bfb0d9a95dce253dae304878e07b076e`, on x86_64-linux. Evaluation produces
the same derivation paths as Nix. Dynamic builds, direct `outputOf` installables,
warm reuse, and embedded-file invalidation pass with IFD and classic-command
fallback disabled. Using iets inside the recursive planner also produces the
same Go package output as the Nix evaluator.

The nested invocation needs a socket adapter: that PR does not read `NIX_REMOTE`,
and its classic instantiator rejects `--store` pointing at a custom Unix socket.
[tests/iets.nix](tests/iets.nix) translates recursive Nix's Unix URL to
`NIX_DAEMON_SOCKET_PATH`. Normal outer `iets build` usage needs no adapter.
The production planner continues to use the pinned Nix evaluator by default.

Five alternating warm evaluation trials, with iets's tracing cache disabled
and its compiled-file cache warm, measured:

| Evaluation | Determinate Nix 3.24.1 | iets, 1 worker | iets, 4 workers |
| --- | ---: | ---: | ---: |
| Dynamic fixture, outer evaluation | 394 ms | 182 ms | 106 ms |
| Exported fixture, 67 units | 56 ms | 20 ms | 29 ms |
| Synthetic exported graph, 1,061 units | 174 ms | 58 ms | 73 ms |

These timings exclude planning, compilation, realization, and network transfers.
More evaluator workers help the outer Nixpkgs expression; one worker was faster
for the generated package graph. See the [raw measurements](benchmarks/ci-2026-11-10.json).
To repeat the compatibility checks and outer evaluation measurements:

```sh
iets=$(nix build github:replit/iets/69883a64bfb0d9a95dce253dae304878e07b076e \
  --no-link --print-out-paths)
python3 tests/iets.py --iets "$iets/bin/iets" --runs 5
```

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
falls back to the pinned `toolchain.nix` only when it resolves to the same
compiler. Custom compilers are never silently replaced. An explicit Nix `toolchain` argument
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
