{ pkgs }:
let
  helpers = pkgs.runCommand "go-libnix-helpers" { nativeBuildInputs = [ pkgs.go ]; } ''
    export HOME=$TMPDIR GOCACHE=$TMPDIR/cache GOTOOLCHAIN=local
    mkdir -p $out/bin
    go build -trimpath -o $out/bin/libnix-buildid cmd/buildid
    go build -trimpath -o $out/bin/libnix-pack cmd/pack
  '';
in pkgs.symlinkJoin {
  name = "go-libnix-toolchain";
  passthru = { inherit pkgs; };
  paths = [ helpers pkgs.go pkgs.stdenv.cc pkgs.bash pkgs.coreutils pkgs.findutils pkgs.gnused pkgs.pkg-config ];
}
