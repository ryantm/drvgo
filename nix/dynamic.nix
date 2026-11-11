# Source-only, package-granular Go builds. Requires dynamic-derivations,
# ca-derivations and recursive-nix in both the client and build daemon.
{ toolchain ? import ../internal/export/templates/toolchain.nix
, drvgo ? import ./package.nix { pkgs = toolchain.pkgs; }
}:
{ src, package ? ".", vendorHash ? null, tags ? "", ldflags ? "", name ? "drvgo", experimentalGo ? false, test ? false, race ? false, cgoEnabled ? null }:
let
  pkgs = toolchain.pkgs;
  inherit (pkgs) lib;
  modules = ((pkgs.buildGoModule.override { go = toolchain.go; }) {
    pname = "drvgo-modules";
    version = "0";
    inherit src vendorHash;
    proxyVendor = true;
  }).goModules;
  # Preserve the original output context, so generated package derivations
  # have the same toolchain dependency as ordinary exports.
  outputExpr = tc: ''builtins.appendContext ${builtins.toJSON (toString tc)} {
    ${builtins.toJSON (builtins.unsafeDiscardOutputDependency tc.drvPath)} = { outputs = [ "out" ]; };
  }'';
  toolchainExpr = ''{
    outPath = ${outputExpr toolchain};
    compile = ${outputExpr (toolchain.compile or toolchain)};
  }'';
  # These wrappers only run explicit tools; a stdenv setup and its dependency
  # closure are unnecessary on the planning and final forwarding paths.
  planner = builtins.derivation {
    name = "${name}-result.drv";
    system = pkgs.stdenv.hostPlatform.system;
    builder = "${pkgs.bash}/bin/bash";
    args = [ "-euo" "pipefail" "-c" plannerScript ];
    PATH = lib.makeBinPath [ drvgo pkgs.nix pkgs.coreutils ];
    # Remote build hooks do not reliably copy newly generated derivation closures.
    # Plan locally; the generated package derivations can still build remotely.
    preferLocalBuild = true;
    __contentAddressed = true;
    outputHashMode = "text";
    outputHashAlgo = "sha256";
    requiredSystemFeatures = [ "recursive-nix" ];
    # Make the toolchain derivation itself available to the nested evaluator.
    toolchainDrv = builtins.unsafeDiscardOutputDependency toolchain.drvPath;
    compileToolchainDrv = builtins.unsafeDiscardOutputDependency (toolchain.compile or toolchain).drvPath;
    coreutilsDrv = builtins.unsafeDiscardOutputDependency pkgs.coreutils.drvPath;
    inherit src;
    passAsFile = [ "expression" ];
    # The emitted derivation name must match the text-addressed .drv output.
    # A forwarding derivation supplies a stable name independent of Go import paths.
    expression = ''
      let tc = ${toolchainExpr};
          coreutils = ${outputExpr pkgs.coreutils};
          command = (import ./graph { toolchain = tc; }).default;
      in derivation {
        name = ${builtins.toJSON "${name}-result"};
        system = "${pkgs.stdenv.hostPlatform.system}";
        # Creating a symlink is cheaper than querying caches or shipping its
        # inputs to a remote builder. Go package builds remain distributable.
        preferLocalBuild = true;
        allowSubstitutes = false;
        builder = "''${coreutils}/bin/ln";
        args = [ "-s" command (builtins.placeholder "out") ];
      }
    '';
  };
  plannerScript = ''
    export HOME=$TMPDIR/home
    mkdir -p "$HOME"
    export NIX_CONFIG='extra-experimental-features = nix-command ca-derivations dynamic-derivations recursive-nix
    allow-import-from-derivation = false'
    drvgo export -C "$src" -out "$TMPDIR/graph" ${lib.optionalString experimentalGo "-experimental-go"} \
      ${lib.optionalString test "-test"} ${lib.optionalString race "-race"} \
      ${lib.optionalString (cgoEnabled != null) "-cgo ${if cgoEnabled then "1" else "0"}"} \
      -toolchain ${toolchain} \
      -module-proxy ${lib.escapeShellArg (if modules == null then "off" else "file://${modules}")} \
      -tags ${lib.escapeShellArg tags} -ldflags ${lib.escapeShellArg ldflags} \
      ${lib.escapeShellArg package}
    cp "$expressionPath" "$TMPDIR/build.nix"
    drv=$(nix-instantiate "$TMPDIR/build.nix")
    cp "$drv" "$out"
  '';
  result = builtins.derivation {
    inherit name;
    system = pkgs.stdenv.hostPlatform.system;
    preferLocalBuild = true;
    allowSubstitutes = false;
    builder = "${pkgs.coreutils}/bin/ln";
    args = [ "-s" (builtins.outputOf planner.outPath "out") (builtins.placeholder "out") ];
  };
in result // { inherit planner modules; passthru = { inherit planner modules; }; }
