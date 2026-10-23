#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
root=$PWD
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
cp -R tests/fixture "$work/source"
chmod -R u+w "$work/source"
# NIX_REMOTE may select a test daemon; ordinary use requires daemon features.
flags=(--extra-experimental-features 'ca-derivations dynamic-derivations recursive-nix'
  --option allow-import-from-derivation false --max-jobs 4)
build() {
  nix-build "${flags[@]}" --out-link "$work/result" --expr \
    "(import $root/nix/dynamic.nix {}) { name = \"drvgo-fixture\"; src = $work/source; }"
}
first=$(build)
"$first/bin/libnix-fixture" > "$work/actual"
printf 'hello embedded world\nstable\nnested asset\n01 02 ff\nhidden asset\n' > "$work/expected"
cmp "$work/expected" "$work/actual"
test "$(build)" = "$first"
# Follow the two wrappers to inspect only generated Go package derivations.
packages() {
  nix-store -qR --include-outputs "$(nix-store -qd "$(readlink -f "$1")")" |
    sed -n '/-go-.*\.drv$/p' | sort
}
packages "$first" > "$work/before"
printf 'changed embed\n' > "$work/source/message/assets/message.txt"
second=$(build)
"$second/bin/libnix-fixture" > "$work/actual"
printf 'changed embed\nstable\nnested asset\n01 02 ff\nhidden asset\n' > "$work/expected"
cmp "$work/expected" "$work/actual"
packages "$second" > "$work/after"
test "$(comm -23 "$work/before" "$work/after" | wc -l)" -eq 2
test "$(comm -13 "$work/before" "$work/after" | wc -l)" -eq 2
printf 'Dynamic build passed: IFD disabled, embeds and cgo verified, exactly two packages invalidated.\n'
