# cmd/go still invokes tools with -V=full during -n discovery. Preserve exact
# responses from the selected SDK without shipping the executable compilers.
# Keep all source and library data so package and embed selection is unchanged.
{ pkgs, go, drvgo }:
let
  target = "${pkgs.stdenv.hostPlatform.go.GOOS}_${pkgs.stdenv.hostPlatform.go.GOARCH}";
in pkgs.runCommand "go-libnix-planning-toolchain" {
  disallowedReferences = [ go ];
} ''
  mkdir -p $out/bin $out/share/go/bin $out/share/go/pkg/tool/${target}
  cp ${go}/share/go/bin/go $out/share/go/bin/go
  ln -s ../share/go/bin/go $out/bin/go
  cp -r ${go}/share/go/{src,lib,go.env,VERSION} $out/share/go/
  for entry in ${go}/share/go/pkg/*; do
    if [ "$(basename "$entry")" != tool ]; then
      cp -r "$entry" $out/share/go/pkg/
    fi
  done
  for tool in asm cgo compile link preprofile; do
    ${go}/share/go/pkg/tool/${target}/$tool -V=full > $out/share/go/pkg/tool/${target}/$tool.version
    ln -s ${drvgo}/bin/drvgo $out/share/go/pkg/tool/${target}/$tool
  done
''
