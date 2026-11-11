{ system ? builtins.currentSystem, toolchainAttr ? "toolchain-go1_26"
, src ? ./fixture, test ? false, race ? false, cgoEnabled ? null
}:
let
  drvgo = builtins.getFlake (toString ../.);
  toolchain = drvgo.packages.${system}.${toolchainAttr};
in drvgo.lib.mkGoPackage {
  inherit system src test race cgoEnabled;
  go = toolchain.go;
  name = "drvgo-compatibility-fixture";
}
