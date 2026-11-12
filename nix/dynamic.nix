# Source-only, package-granular Go builds. Requires dynamic-derivations,
# ca-derivations and recursive-nix in both the client and build daemon.
{ toolchain ? import ../internal/export/templates/toolchain.nix
, drvgo ? import ./package.nix { pkgs = toolchain.pkgs; }
}:
{ src, package ? ".", vendorHash ? null, tags ? "", ldflags ? "", name ? "drvgo"
, experimentalGo ? false, test ? false, race ? false, cgoEnabled ? null
, allowPlannerSubstitutes ? false, allowEmptyTests ? false
, runtimeInputs ? []
, previousPlan ? null
, moduleCache ? null
, prepareModules ? false
, exportData ? false
, earlyCutoff ? false
, iets ? null
}:
assert !earlyCutoff || exportData;
let
  pkgs = toolchain.pkgs;
  inherit (pkgs) lib;
  modules = ((pkgs.buildGoModule.override { go = toolchain.go; }) {
    pname = "drvgo-modules";
    version = "0";
    inherit src vendorHash;
    proxyVendor = true;
  }).goModules;
  preparedModules = if moduleCache != null then moduleCache
    else if !prepareModules || modules == null then null
    else import ./module-cache.nix { inherit pkgs drvgo modules; go = toolchain.go; };
  # Only the recipe is needed during planning. Restore its output context in
  # the nested evaluator, so generated packages keep the same dependencies as
  # ordinary exports without making the planner fetch unused worker tools.
  outputExpr = tc: ''builtins.appendContext ${builtins.toJSON (builtins.unsafeDiscardStringContext (toString tc))} {
    ${builtins.toJSON (builtins.unsafeDiscardOutputDependency tc.drvPath)} = { outputs = [ "out" ]; };
  }'';
  toolchainExpr = ''{
    outPath = ${outputExpr toolchain};
    compile = ${outputExpr (toolchain.compile or toolchain)};
    link = ${outputExpr (toolchain.link or toolchain)};
    native = ${outputExpr (toolchain.native or toolchain)};
  }'';
  # Dry-run discovery needs the driver, sources and tool-version metadata.
  # Native compiler probes are emitted for workers to replay instead.
  # Empty-test validation can compile and execute a selector or compile a
  # no-test package, including its cgo dependencies. Keep actual compilers
  # available for that path; explicitly disabling cgo avoids native tools.
  planningToolchain = if allowEmptyTests then
    (if cgoEnabled == false then toolchain.go else toolchain)
  else
    import ./planning-toolchain.nix { inherit pkgs drvgo; go = toolchain.go; };
  # These wrappers only run explicit tools; a stdenv setup and its dependency
  # closure are unnecessary on the planning and final forwarding paths.
  planner = builtins.derivation ({
    name = "${name}-result.drv";
    system = pkgs.stdenv.hostPlatform.system;
    builder = "${pkgs.bash}/bin/bash";
    args = [ "-euo" "pipefail" "-c" plannerScript ];
    # The exporter queries the recursive daemon directly. Only the default
    # evaluator needs Nix's command-line tools in the planner's input closure.
    PATH = lib.makeBinPath ([ drvgo ] ++ lib.optional (iets == null) pkgs.nix ++ [ pkgs.coreutils ]);
    # Remote build hooks do not reliably copy newly generated derivation closures.
    # Plan locally; the generated package derivations can still build remotely.
    preferLocalBuild = true;
    # A planner cache hit would copy its generated derivation/source closure.
    # Replanning is cheap; spend cache lookups on reusable package outputs.
    allowSubstitutes = allowPlannerSubstitutes;
    __contentAddressed = true;
    outputHashMode = "text";
    outputHashAlgo = "sha256";
    requiredSystemFeatures = [ "recursive-nix" ];
    # Make the toolchain derivation itself available to the nested evaluator.
    toolchainDrv = builtins.unsafeDiscardOutputDependency toolchain.drvPath;
    compileToolchainDrv = builtins.unsafeDiscardOutputDependency (toolchain.compile or toolchain).drvPath;
    linkToolchainDrv = builtins.unsafeDiscardOutputDependency (toolchain.link or toolchain).drvPath;
    nativeToolchainDrv = builtins.unsafeDiscardOutputDependency (toolchain.native or toolchain).drvPath;
    drvgoDrv = builtins.unsafeDiscardOutputDependency drvgo.drvPath;
    inherit src;
    passAsFile = [ "expression" ];
    # The emitted derivation name must match the text-addressed .drv output.
    # A forwarding derivation supplies a stable name independent of Go import paths.
    expression = ''
      let tc = ${toolchainExpr};
          drvgo = ${outputExpr drvgo};
          command = (import ./graph {
            toolchain = tc;
            exportData = ${builtins.toJSON exportData};
            earlyCutoff = ${builtins.toJSON earlyCutoff};
          }).default;
      in derivation {
        name = ${builtins.toJSON "${name}-result"};
        system = "${pkgs.stdenv.hostPlatform.system}";
        # Creating a symlink is cheaper than querying caches or shipping its
        # inputs to a remote builder. Go package builds remain distributable.
        preferLocalBuild = true;
        allowSubstitutes = false;
        # Unlike coreutils ln, the native helper leaves stdout/stderr open
        # until exit, avoiding Nix's EOF-triggered builder cleanup race.
        builder = "''${drvgo}/bin/drvgo";
        args = [ "internal-symlink" command (builtins.placeholder "out") ];
      }
    '';
  } // lib.optionalAttrs (runtimeInputs != []) {
    # Literal paths in an existing checkout have no Nix string context.
    # Make their data available to the restricted recursive store; the graph
    # retains only references found in each selected package or replay action.
    inherit runtimeInputs;
    passAsFile = [ "expression" "runtimeInputs" ];
  } // lib.optionalAttrs (previousPlan != null) {
    # Make an earlier graph's closure visible to the restricted store. Source
    # discovery still runs from scratch; only identical content-addressed
    # sources and derivation recipes can reuse their existing registrations.
    inherit previousPlan;
  });
  plannerScript = ''
    export HOME=$TMPDIR/home
    mkdir -p "$HOME"
    export NIX_CONFIG='extra-experimental-features = nix-command ca-derivations dynamic-derivations recursive-nix
    allow-import-from-derivation = false'
    drvgo export -C "$src" -out "$TMPDIR/graph" ${lib.optionalString experimentalGo "-experimental-go"} \
      ${lib.optionalString test "-test"} ${lib.optionalString race "-race"} \
      ${lib.optionalString allowEmptyTests "-allow-empty-tests"} \
      ${lib.optionalString (cgoEnabled != null) "-cgo ${if cgoEnabled then "1" else "0"}"} \
      -toolchain ${planningToolchain} \
      ${if preparedModules != null then "-module-cache ${lib.escapeShellArg (toString preparedModules)}"
        else "-module-proxy ${lib.escapeShellArg (if modules == null then "off" else "file://${modules}")}"} \
      -tags ${lib.escapeShellArg tags} -ldflags ${lib.escapeShellArg ldflags} \
      ${lib.escapeShellArg package}
    cp "$expressionPath" "$TMPDIR/build.nix"
    ${if iets == null then ''drv=$(nix-instantiate "$TMPDIR/build.nix")'' else ''
      drv=$(
        export IETS_NIX_FALLBACK=never IETS_PROGRESS=false IETS_WORKERS=1 IETS_CACHE=false
        # iets PR #3's classic evaluator uses the local-daemon transport.
        # Route it to the restricted recursive store supplied by this build.
        case "''${NIX_REMOTE:-}" in
          unix://*) export NIX_DAEMON_SOCKET_PATH="''${NIX_REMOTE#unix://}" ;;
          *) echo "expected a recursive Nix Unix socket, got ''${NIX_REMOTE:-unset}" >&2; exit 1 ;;
        esac
        exec -a nix-instantiate ${iets}/bin/iets "$TMPDIR/build.nix"
      )
    ''}
    ${if pkgs.stdenv.hostPlatform.isDarwin then ''
      # macOS cannot extend the sandbox's filesystem permissions for a newly
      # registered derivation. Read it through the restricted daemon instead.
      drvgo internal-copy-derivation "$drv" "$out"
    '' else ''
      cp "$drv" "$out"
    ''}
  '';
  result = builtins.derivation {
    inherit name;
    system = pkgs.stdenv.hostPlatform.system;
    preferLocalBuild = true;
    allowSubstitutes = false;
    builder = "${drvgo}/bin/drvgo";
    args = [ "internal-symlink" (builtins.outputOf planner.outPath "out") (builtins.placeholder "out") ];
  };
in result // { inherit planner modules planningToolchain; moduleCache = preparedModules;
  passthru = { inherit planner modules planningToolchain; moduleCache = preparedModules; }; }
