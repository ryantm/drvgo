# Replit UPM validation

Run from the repository root:

```sh
examples/upm/test.sh
examples/upm/result/bin/upm --version
```

The script creates a local checkout under `work/upm`, builds both Go commands with `drvgo`, generates the Python package database in a Nix derivation, and wraps UPM with `PYPI_MAP_DB`. It does not install UPM or its language package managers system-wide.

Tested on 2026-10-22, x86_64-linux, with Nix 2.34.8 and the pinned Go 1.26.5 toolchain. Nix sandboxing was enabled. The upstream checkout was [replit/upm at `35a76c282507fb942445c0ee98427b3d1ef772bc`](https://github.com/replit/upm/tree/35a76c282507fb942445c0ee98427b3d1ef772bc); no upstream source modifications were needed.

| Check | Result |
| --- | --- |
| `cmd/upm` | Built: 554 package derivations |
| `internal/backends/python/gen_pypi_map` | Built: 710 package derivations |
| Packages common to both commands | All 522 had identical derivation paths |
| Combined package graph | 742 distinct package derivations |
| Unchanged regeneration/build | Same output path; no package builds |
| Fresh checkout at a different path | Reused the same command and generator outputs |
| `upm --version` | `upm libnix-go-test` |
| `upm --help`, `upm list-languages` | Passed |
| JavaScript `require("lodash")` | `upm --lang nodejs-npm guess --all` returned `lodash` |
| Python `import requests` | `upm --lang python3-pip guess --all` returned `requests` |
| Database integrity | SQLite `integrity_check`: `ok` |
| Generated database | 148,615 import mappings; 136,197 packages |
| Packaged runtime closure | 106.1 MiB on this platform |

These are build and targeted runtime smoke tests, not UPM's complete upstream integration suite. Most of its language backends require separately installed package managers. The upstream database generator iterates Go maps; bit-for-bit reproducibility of that database was not tested.

For the Go backend itself, the real-Nix integration fixture additionally checks string, byte-slice, nested filesystem, glob, and `all:` hidden-file embeds; literal asset names with spaces and `$`; parent-directory C headers and C symbols shared between packages; unchanged-build cache reuse; exactly two invalidated derivations after an embed edit; and building the changed export after deleting its original sources. The fixture has 67 package derivations.

Implementation issues found through UPM were fixed in the backend: Go's printed JSON heredoc delimiter, relative compiler input paths, directory/file-prefix collisions (`version/` versus `version.go`), native parent-directory includes, cgo's `dynimportfail` external-link fallback, and redundant directory changes that prevented cross-command caching.

The build recipe is [package.nix](package.nix); the executable test is [test.sh](test.sh). Generated snapshots and checkout data are ignored by the top-level `.gitignore`.

### Dynamic derivations

`nix-build dynamic.nix -A package --option allow-import-from-derivation false`
builds directly from the pinned GitHub source, with no checkout or exported graph.
Enable `ca-derivations`, `dynamic-derivations`, and `recursive-nix` in the client
and daemon, and advertise `recursive-nix` on the builder (see the root README).

Verified with upstream Nix 2.34.8 and Determinate Nix 3.24.0, with sandboxing
enabled: both graphs built,
JavaScript `require("lodash")` resolved to `lodash`, Python `import requests`
resolved to `requests`, and SQLite integrity passed. An unchanged cached build
returned the same output in approximately 0.6 seconds on the test machine.
No clean cold-build benchmark was recorded. The raw command's version is `unknown version` unless `ldflags` sets it.

Also verified from `replit2` with Determinate Nix 3.24.0: local sandboxed
planning, remote package compilation on `replit1`, IFD disabled, and the same
JavaScript/Python smoke tests. Use `--max-jobs 1` on clients whose system
configuration disables local builds. Fully remote planning failed because the
remote build hook did not transfer the generated dependency derivations.

### Incremental rebuild measurement

Before publication as drvgo, a temporary checkout was edited to add a diagnostic
print to `cmd/upm/main.go`. On the same x86_64-linux machine, with Determinate
Nix 3.24.0, IFD disabled, and at most four local jobs:

| Store-backed source build | Wall time |
| --- | --- |
| Unchanged, cached | 0.72 s |
| After the edit | 18.50 s |
| Unchanged after the edit | 0.79 s |

Both planners reran, but only the command's Go derivation compiled and linked;
all other Go package outputs were reused. The Python database regenerated
because its inputs referred to the changed source-tree store path. With a local
Nix path as the source, the edit took 17.73 s and reused the database. JavaScript
and Python smoke tests passed after the edit. These are single-run incremental
measurements, not cold-build benchmarks.
