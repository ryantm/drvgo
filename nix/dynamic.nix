# Source-only, package-granular Go builds. Requires dynamic-derivations,
# ca-derivations and recursive-nix in both the client and build daemon.
{ toolchain ? import ../internal/export/templates/toolchain.nix
, drvgo ? import ./package.nix { pkgs = toolchain.pkgs; }
}:
{ src, package ? ".", vendorHash ? null, tags ? "", ldflags ? "", name ? "drvgo", experimentalGo ? false }:
let
  pkgs = toolchain.pkgs;
  inherit (pkgs) lib;
  modules = (pkgs.buildGoModule {
    pname = "drvgo-modules";
    version = "0";
    inherit src vendorHash;
    proxyVendor = true;
  }).goModules;
  # Preserve the original output context, so generated package derivations
  # have the same toolchain dependency as ordinary exports.
  toolchainExpr = ''builtins.appendContext ${builtins.toJSON (toString toolchain)} {
    ${builtins.toJSON (builtins.unsafeDiscardOutputDependency toolchain.drvPath)} = { outputs = [ "out" ]; };
  }'';
  planner = pkgs.runCommand "${name}-result.drv" {
    # Remote build hooks do not reliably copy newly generated derivation closures.
    # Plan locally; the generated package derivations can still build remotely.
    preferLocalBuild = true;
    __contentAddressed = true;
    outputHashMode = "text";
    outputHashAlgo = "sha256";
    requiredSystemFeatures = [ "recursive-nix" ];
    nativeBuildInputs = [ drvgo pkgs.nix ];
    # Make the toolchain derivation itself available to the nested evaluator.
    toolchainDrv = builtins.unsafeDiscardOutputDependency toolchain.drvPath;
    inherit src;
    passAsFile = [ "expression" ];
    # The emitted derivation name must match the text-addressed .drv output.
    # A forwarding derivation supplies a stable name independent of Go import paths.
    expression = ''
      let tc = ${toolchainExpr};
          command = (import ./graph { toolchain = tc; }).default;
      in derivation {
        name = "${name}-result";
        system = "${pkgs.stdenv.hostPlatform.system}";
        builder = "''${tc}/bin/bash";
        args = [ "-c" "''${tc}/bin/ln -s $command $out" ];
        inherit command;
      }
    '';
  } ''
    export HOME=$TMPDIR/home
    mkdir -p "$HOME"
    export NIX_CONFIG='extra-experimental-features = nix-command ca-derivations dynamic-derivations recursive-nix
    allow-import-from-derivation = false'
    drvgo export -C "$src" -out "$TMPDIR/graph" ${lib.optionalString experimentalGo "-experimental-go"} \
      -toolchain ${toolchain} \
      -module-proxy ${lib.escapeShellArg (if modules == null then "off" else "file://${modules}")} \
      -tags ${lib.escapeShellArg tags} -ldflags ${lib.escapeShellArg ldflags} \
      ${lib.escapeShellArg package}
    cp "$expressionPath" "$TMPDIR/build.nix"
    drv=$(nix-instantiate "$TMPDIR/build.nix")
    cp "$drv" "$out"
  '';
  result = pkgs.runCommand name { passthru = { inherit planner modules; }; } ''
    ln -s ${builtins.outputOf planner.outPath "out"} "$out"
  '';
in result
