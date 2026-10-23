let
  source = builtins.fetchTree {
    type = "github";
    owner = "NixOS";
    repo = "nixpkgs";
    rev = "6b5e5b7a6631f065bf6908986990b37d845f847f";
    narHash = "sha256-smTKQXMLLStzc8zJevMCckbk3My7SvbbLmPYZUJJKW4=";
  };
  pkgs = import source.outPath {};
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
