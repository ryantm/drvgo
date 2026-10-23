#!/usr/bin/env bash
set -euo pipefail
repo=$(cd -- "$(dirname -- "$0")/../.." && pwd)
cd "$repo"
revision=35a76c282507fb942445c0ee98427b3d1ef772bc
checkout="$repo/examples/upm/work/upm"
mkdir -p "$(dirname "$checkout")"
if [ ! -d "$checkout/.git" ]; then
  git clone --no-checkout https://github.com/replit/upm "$checkout"
  git -C "$checkout" checkout --detach "$revision"
fi
test "$(git -C "$checkout" rev-parse HEAD)" = "$revision"
# Build the driver with the same project-local Go toolchain used for planning.
nix-shell --run 'go build -o bin/drvgo ./cmd/drvgo'
bin/drvgo build -C "$checkout" \
  -ldflags '-X github.com/replit/upm/internal/cli.version=drvgo-test' ./cmd/upm
bin/drvgo build -C "$checkout" -out .drvgo-generator -o result-generator \
  ./internal/backends/python/gen_pypi_map
nix-build examples/upm/package.nix -A package -o examples/upm/result
upm="$repo/examples/upm/result/bin/upm"
test "$("$upm" --version)" = 'upm drvgo-test'
"$upm" --help > "$repo/examples/upm/work/help.txt"
"$upm" list-languages > "$repo/examples/upm/work/languages.txt"
smoke=$(mktemp -d)
trap 'rm -rf "$smoke"' EXIT
mkdir -p "$smoke/js" "$smoke/python"
printf '{"name":"drvgo-smoke","version":"1.0.0","dependencies":{}}\n' > "$smoke/js/package.json"
printf 'const lodash = require("lodash");\n' > "$smoke/js/index.js"
printf 'import requests\n' > "$smoke/python/main.py"
test "$(cd "$smoke/js" && "$upm" --lang nodejs-npm guess --all)" = lodash
test "$(cd "$smoke/python" && "$upm" --lang python3-pip guess --all)" = requests
# An unchanged regeneration must retain the same Nix output.
before=$(readlink "$checkout/result")
bin/drvgo build -C "$checkout" \
  -ldflags '-X github.com/replit/upm/internal/cli.version=drvgo-test' ./cmd/upm
test "$(readlink "$checkout/result")" = "$before"
printf 'UPM smoke tests and unchanged rebuild passed: %s\n' "$upm"
