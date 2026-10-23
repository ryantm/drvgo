{
  description = "drvgo: package-granular Go builds with Nix";

  # Keep this aligned with the standalone export's Go 1.26.5 toolchain.
  inputs.nixpkgs.url = "github:NixOS/nixpkgs/6b5e5b7a6631f065bf6908986990b37d845f847f";

  outputs = { self, nixpkgs }:
    let
      systems = [ "x86_64-linux" "aarch64-linux" ];
      forAllSystems = nixpkgs.lib.genAttrs systems;
      packageSets = forAllSystems (system: import nixpkgs { inherit system; });
      toolchains = forAllSystems (system:
        import ./internal/export/templates/toolchain-builder.nix {
          pkgs = packageSets.${system};
        });
    in {
      packages = forAllSystems (system: rec {
        drvgo = import ./nix/package.nix { pkgs = packageSets.${system}; };
        default = drvgo;
        toolchain-go1_25 = self.lib.mkToolchain { inherit system; go = packageSets.${system}.go_1_25; };
        toolchain-go1_26 = toolchains.${system};
        toolchain-go1_26_6 = self.lib.mkToolchain {
          inherit system;
          go = import ./nix/go-1.26.6.nix { pkgs = packageSets.${system}; };
        };
      });
      apps = forAllSystems (system: rec {
        drvgo = {
          type = "app";
          program = "${self.packages.${system}.drvgo}/bin/drvgo";
          meta.description = "Package-granular Go builds with Nix";
        };
        default = drvgo;
      });
      checks = forAllSystems (system: {
        drvgo = self.packages.${system}.drvgo;
      });
      # Call with { system; src; package; vendorHash; ... }.
      # Only invoking this helper requires the dynamic Nix features.
      lib.mkToolchain = { system, go ? packageSets.${system}.go }:
        import ./internal/export/templates/toolchain-builder.nix {
          pkgs = packageSets.${system};
          inherit go;
        };
      lib.mkGoPackage = { system, go ? packageSets.${system}.go, ... }@args:
        (import ./nix/dynamic.nix {
          toolchain = self.lib.mkToolchain { inherit system go; };
          drvgo = self.packages.${system}.drvgo;
        }) (builtins.removeAttrs args [ "system" "go" ]);
    };
}
