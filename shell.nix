let toolchain = import ./internal/export/templates/toolchain.nix;
in toolchain.pkgs.mkShell { packages = [ toolchain ]; }
