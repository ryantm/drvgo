{ system ? builtins.currentSystem, toolchainAttr ? "toolchain-go1_26"
, src ? ./fixture, test ? false, race ? false, cgoEnabled ? null
, allowPlannerSubstitutes ? false, allowEmptyTests ? false
, package ? ".", ldflags ? ""
}:
let
  drvgo = builtins.getFlake (toString ../.);
  toolchain = drvgo.packages.${system}.${toolchainAttr};
in drvgo.lib.mkGoPackage {
  inherit system src test race cgoEnabled allowPlannerSubstitutes allowEmptyTests package ldflags;
  go = toolchain.go;
  name = "drvgo-compatibility-fixture";
}
