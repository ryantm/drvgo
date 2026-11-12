{ pkgs, go ? pkgs.go
, runnerSource ? builtins.toFile "drvgo-builder.go" (builtins.readFile ./runner.go)
, buildidSource ? builtins.toFile "drvgo-buildid.go" (builtins.readFile ./buildid.go)
, packSource ? builtins.toFile "drvgo-pack.go" (builtins.readFile ./pack.go)
, packPatch ? builtins.toFile "drvgo-pack.patch" (builtins.readFile ./pack.patch)
}:
let
  target = "${pkgs.stdenv.hostPlatform.go.GOOS}_${pkgs.stdenv.hostPlatform.go.GOARCH}";
  # Copy executables instead of symlinking the SDK: workers do not need the
  # driver, documentation, other tools, or the entire standard-library source.
  tools = names: pkgs.runCommand "go-libnix-${builtins.concatStringsSep "-" names}" {
    disallowedReferences = [ go ];
  } ''
    mkdir -p $out/share/go/pkg/tool/${target}
    for tool in ${builtins.concatStringsSep " " names}; do
      cp ${go}/share/go/pkg/tool/${target}/$tool $out/share/go/pkg/tool/${target}/
    done
    cp -r ${go}/share/go/pkg/include $out/share/go/pkg/
  '';
  overlay = builtins.toFile "drvgo-builder-overlay.json" (builtins.toJSON {
    Replace = {
      # Attribute names cannot carry context; nativeBuildInputs retains Go.
      "${builtins.unsafeDiscardStringContext (toString go)}/share/go/src/cmd/drvgo-builder/runner.go" = runnerSource;
      "${builtins.unsafeDiscardStringContext (toString go)}/share/go/src/cmd/drvgo-builder/buildid.go" = buildidSource;
      "${builtins.unsafeDiscardStringContext (toString go)}/share/go/src/cmd/drvgo-builder/pack.go" = packSource;
      "${builtins.unsafeDiscardStringContext (toString go)}/share/go/src/cmd/drvgo-builder/pack-upstream.go" = "@DRVGO_PACK_SOURCE@";
    };
  });
  helpers = pkgs.runCommand "go-libnix-helpers" {
    nativeBuildInputs = [ go ];
    disallowedReferences = [ go ];
  } ''
    export HOME=$TMPDIR GOCACHE=$TMPDIR/cache GOTOOLCHAIN=local CGO_ENABLED=0
    mkdir -p $out/bin
    # Reuse this exact SDK's archive format and object handling. The patch only
    # gives main a callable entry point, closes its archive, and drops telemetry.
    cp ${go}/share/go/src/cmd/pack/pack.go "$TMPDIR/pack-upstream.go"
    patch --silent --fuzz=0 "$TMPDIR/pack-upstream.go" < ${packPatch}
    substitute ${overlay} "$TMPDIR/overlay.json" \
      --subst-var-by DRVGO_PACK_SOURCE "$TMPDIR/pack-upstream.go"
    go build -trimpath -ldflags='-s -w' -tags drvgo_toolchain \
      -overlay "$TMPDIR/overlay.json" -o $out/bin/drvgo-builder cmd/drvgo-builder
  '';
  compile = pkgs.symlinkJoin {
    name = "go-libnix-compile-toolchain";
    paths = [ helpers (tools [ "compile" "asm" ]) ];
  };
  link = pkgs.symlinkJoin {
    name = "go-libnix-link-toolchain";
    paths = [ compile (tools [ "link" ]) ];
  };
  # Our builders do not source stdenv setup. Keep the Darwin resolver's
  # search path in the compiler wrapper, for both discovery and replay.
  cc = if pkgs.stdenv.hostPlatform.isDarwin then
    pkgs.stdenv.cc.override (old: {
      extraBuildCommands = (old.extraBuildCommands or "") + ''
        echo '-L${pkgs.lib.getLib pkgs.darwin.libresolv}/lib' >> $out/nix-support/cc-ldflags
      '';
    })
  else pkgs.stdenv.cc;
  native = pkgs.symlinkJoin {
    name = "go-libnix-native-toolchain";
    # Replay handles file operations itself. The compiler wrappers retain their
    # own runtime utilities; pkg-config dependencies are unsupported.
    paths = [ link (tools [ "cgo" ]) cc ];
    postBuild = pkgs.lib.optionalString pkgs.stdenv.hostPlatform.isDarwin ''
      ln -s ${pkgs.lib.getBin pkgs.stdenv.cc.bintools.bintools}/bin/dsymutil $out/bin/dsymutil
    '';
  };
in pkgs.symlinkJoin {
  name = "go-libnix-toolchain";
  passthru = { inherit pkgs go compile link native; };
  # Only discovery uses cmd/go and the complete SDK. The generated graph
  # substitutes worker tools and snapshots each selected standard package.
  paths = [ native go ];
  postBuild = ''
    mkdir -p $out/nix-support
    echo ${compile} > $out/nix-support/drvgo-compile-toolchain
    echo ${link} > $out/nix-support/drvgo-link-toolchain
    echo ${native} > $out/nix-support/drvgo-native-toolchain
  '';
}
