{ system ? builtins.currentSystem, toolchainAttr ? "toolchain-go1_26" }:
let
  drvgo = builtins.getFlake (toString ../.);
  toolchain = drvgo.packages.${system}.${toolchainAttr};
in drvgo.lib.mkGoPackage {
  inherit system;
  go = toolchain.go;
  src = ./fixture;
  name = "drvgo-compatibility-fixture";
}
