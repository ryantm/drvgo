set -euo pipefail
export HOME="$TMPDIR/home"
export GOCACHE="$TMPDIR/cache"
mkdir -p "$HOME" "$GOCACHE" "$TMPDIR/unit" "$TMPDIR/work"
# Quoted heredocs in Go's trace contain literal $WORK references. Replace those
# along with command arguments before executing, not via shell expansion.
sed -e "s|@SELF@|$TMPDIR/unit|g" -e "s|@WORK@|$TMPDIR/work|g" "$scriptPath" > "$TMPDIR/replay.sh"
cd "$TMPDIR/work"
bash -euo pipefail "$TMPDIR/replay.sh" < /dev/null
mkdir -p "$out"
cp "$TMPDIR/unit/_pkg_.a" "$out/_pkg_.a"
if [ "$isBinary" = 1 ]; then
  mkdir -p "$out/bin"
  cp "$TMPDIR/unit/exe/a.out" "$out/bin/$binaryName"
fi
