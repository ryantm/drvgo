# Supply a built iets store output. With nested=true, use its classic evaluator
# inside the recursive planner as well as using iets for the outer build.
{ iets, src ? ./fixture, name ? "drvgo-iets-fixture", nested ? false
, cgoEnabled ? null, test ? false, allowEmptyTests ? false, tags ? "", race ? false
, runtimeInputs ? [], ldflags ? ""
, moduleCache ? null
, exportData ? false, earlyCutoff ? false
, baseToolchain ? import ../internal/export/templates/toolchain.nix
}:
(import ../nix/dynamic.nix { toolchain = baseToolchain; }) {
  inherit src name cgoEnabled test allowEmptyTests tags runtimeInputs ldflags race;
  inherit moduleCache;
  inherit exportData earlyCutoff;
  iets = if nested then iets else null;
}
