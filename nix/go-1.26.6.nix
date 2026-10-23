# An independently pinned patch release for the compatibility matrix.
{ pkgs }:
pkgs.go_1_26.overrideAttrs (old: {
  version = "1.26.6";
  src = pkgs.fetchurl {
    url = "https://go.dev/dl/go1.26.6.src.tar.gz";
    hash = "sha256-oHIcVMaIkBRI13rZs+x+p8R0cwdV/4kTgukuy5P/LLE=";
  };
})
