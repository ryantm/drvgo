{ pkgs, go ? pkgs.go }:
let
  helpers = pkgs.runCommand "go-libnix-helpers" { nativeBuildInputs = [ go ]; } ''
    export HOME=$TMPDIR GOCACHE=$TMPDIR/cache GOTOOLCHAIN=local
    mkdir -p $out/bin
    go build -trimpath -o $out/bin/libnix-buildid cmd/buildid
    go build -trimpath -o $out/bin/libnix-pack cmd/pack
  '';
  # Most package actions only invoke Go tools. Keep the C compiler and its development
  # closure off those builders, particularly when copying inputs remotely.
  compile = pkgs.symlinkJoin {
    name = "go-libnix-compile-toolchain";
    paths = [ helpers go pkgs.bash pkgs.coreutils pkgs.gnused ];
  };
in pkgs.symlinkJoin {
  name = "go-libnix-toolchain";
  passthru = { inherit pkgs go compile; };
  paths = [ compile pkgs.stdenv.cc pkgs.findutils pkgs.pkg-config ];
  postBuild = ''
    mkdir -p $out/nix-support
    echo ${compile} > $out/nix-support/drvgo-compile-toolchain
  '';
}
