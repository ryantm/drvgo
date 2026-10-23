let
  source = builtins.fetchTree {
    type = "github";
    owner = "NixOS";
    repo = "nixpkgs";
    rev = "6b5e5b7a6631f065bf6908986990b37d845f847f";
    narHash = "sha256-smTKQXMLLStzc8zJevMCckbk3My7SvbbLmPYZUJJKW4=";
  };
  pkgs = import source.outPath {};
in import ./toolchain-builder.nix { inherit pkgs; }
