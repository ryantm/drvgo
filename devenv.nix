{ pkgs, ... }: {
  languages.go.enable = true;
  packages = [ pkgs.gcc pkgs.pkg-config pkgs.nix ];
}
