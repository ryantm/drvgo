{ system ? builtins.currentSystem, toolchainAttr ? "toolchain-go1_26"
, src ? ./fixture, test ? false, race ? false, cgoEnabled ? null
, allowPlannerSubstitutes ? false, allowEmptyTests ? false
, package ? ".", ldflags ? ""
, runtimeInputs ? []
, moduleCache ? null, prepareModules ? false
}:
let
  drvgo = builtins.getFlake (toString ../.);
  toolchain = drvgo.packages.${system}.${toolchainAttr};
in drvgo.lib.mkGoPackage {
  inherit system src test race cgoEnabled allowPlannerSubstitutes allowEmptyTests package ldflags runtimeInputs;
  inherit moduleCache prepareModules;
  go = toolchain.go;
  name = "drvgo-compatibility-fixture";
}
