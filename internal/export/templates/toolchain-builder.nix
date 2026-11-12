{ pkgs, go ? pkgs.go
, runnerSource ? builtins.toFile "drvgo-builder.go" (builtins.readFile ./runner.go)
, buildidSource ? builtins.toFile "drvgo-buildid.go" (builtins.readFile ./buildid.go)
, packSource ? builtins.toFile "drvgo-pack.go" (builtins.readFile ./pack.go)
, packPatch ? builtins.toFile "drvgo-pack.patch" (builtins.readFile ./pack.patch)
, ccQuerySource ? builtins.toFile "drvgo-cc-query.c.in" (builtins.readFile ./cc-query.c.in)
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
  # Match cmd/link's hostlinkArchArgs. Internal links query the compiler for
  # runtime archives but do not need its compiler, assembler, or external linker.
  queryArchArgs = if pkgs.stdenv.hostPlatform.isDarwin then [ "-arch" "arm64" ]
    else if pkgs.stdenv.hostPlatform.isx86_64 then [ "-m64" ] else [];
  internalLinkSupport = pkgs.runCommand "go-libnix-internal-link-support" {
    disallowedReferences = [ cc cc.cc ];
  } ''
    mkdir -p $out/bin $out/lib $out/nix-support
    substitute ${ccQuerySource} "$TMPDIR/cc-query.c" \
      --replace-fail '@ARCH_ARGS@' ${pkgs.lib.escapeShellArg (builtins.concatStringsSep "" (map (arg: builtins.toJSON arg + ", ") queryArchArgs))} \
      --replace-fail '@QUERY_ROOT@' "$out/nix-support"
    ${cc}/bin/cc -Os -Wall -Wextra -Werror "$TMPDIR/cc-query.c" -o $out/bin/drvgo-cc-query
    ${pkgs.lib.optionalString pkgs.stdenv.hostPlatform.isLinux ''
      # The wrapper adds GCC runtime search paths even though this helper only
      # needs libc. Retain only paths that satisfy its actual ELF dependencies.
      ${pkgs.patchelf}/bin/patchelf --shrink-rpath $out/bin/drvgo-cc-query
    ''}
    ln -s drvgo-cc-query $out/bin/${if pkgs.stdenv.hostPlatform.isDarwin then "clang" else "gcc"}
    query() {
      library="$(${cc}/bin/cc ${pkgs.lib.escapeShellArgs queryArchArgs} "$1")"
      if [ -f "$library" ]; then
        cp -L "$library" "$out/lib/$2.a"
        library="$out/lib/$2.a"
      fi
      printf '%s\n' "$library" > "$out/nix-support/$2"
    }
    query --print-libgcc-file-name libgcc
    query --print-file-name=libc_nonshared.a libc_nonshared
    query --print-file-name=libssp_nonshared.a libssp_nonshared
  '';
  internalLink = pkgs.symlinkJoin {
    name = "go-libnix-internal-link-toolchain";
    paths = [ link internalLinkSupport ];
  };
  # lto-dump inspects compiler IR; compiling and linking use lto1 instead.
  # Keep the frontends, LTO backend, libraries, headers and diagnostics intact.
  # The runtime-query bundle above keeps its original compiler recipe.
  workerCC = if pkgs.stdenv.hostPlatform.isLinux && (cc.isGNU or false)
    && (cc.cc.outputName or "out") == "out" then
    let
      gcc = cc.cc;
      copied = pkgs.runCommand gcc.name {
        disallowedReferences = [ gcc ];
      } ''
        export LC_ALL=C
        gccOriginal=${gcc}
        cp -a "$gccOriginal" "$out"
        chmod -R u+w "$out"
        rm -f "$out/bin/lto-dump" "$out/bin/"*-lto-dump
        # Preserve the store basename and replace only its fixed-size hash.
        # This relocates embedded compiler search paths without resizing ELF
        # sections or retaining the original compiler in the worker closure.
        test "''${#gccOriginal}" -eq "''${#out}"
        gccOldHash="$(basename "$gccOriginal")"
        gccOldHash="''${gccOldHash%%-*}"
        gccNewHash="$(basename "$out")"
        gccNewHash="''${gccNewHash%%-*}"
        if grep -rlFZ -- "$gccOriginal" "$out" > "$TMPDIR/rewrite-files"; then
          xargs -0 -r sed -i "s|${builtins.storeDir}/$gccOldHash-|${builtins.storeDir}/$gccNewHash-|g" < "$TMPDIR/rewrite-files"
        else
          grepStatus=$?
          test "$grepStatus" -eq 1
        fi
        find "$out" -type l -print0 > "$TMPDIR/rewrite-links"
        while IFS= read -r -d $'\0' gccLink; do
          gccTarget="$(readlink "$gccLink")"
          case "$gccTarget" in
            "$gccOriginal"/*) ln -sfn "$out''${gccTarget#"$gccOriginal"}" "$gccLink" ;;
          esac
        done < "$TMPDIR/rewrite-links"
      '';
      compact = gcc // copied // { inherit (gcc) stdenv; };
    in cc.override (old: {
      cc = compact;
      # Rewrapping the bootstrap compiler otherwise selects its bootstrap
      # build environment. Use the current builder and omit separate manuals.
      stdenvNoCC = pkgs.stdenvNoCC;
      propagateDoc = false;
    } // pkgs.lib.optionalAttrs (old ? gccForLibs && old.gccForLibs != null
      && toString old.gccForLibs == toString gcc) {
      gccForLibs = compact;
    })
  else cc;
  native = pkgs.symlinkJoin {
    name = "go-libnix-native-toolchain";
    # Replay handles file operations itself. The compiler wrappers retain their
    # own runtime utilities; pkg-config dependencies are unsupported.
    paths = [ link (tools [ "cgo" ]) workerCC ];
    postBuild = pkgs.lib.optionalString pkgs.stdenv.hostPlatform.isDarwin ''
      ln -s ${pkgs.lib.getBin pkgs.stdenv.cc.bintools.bintools}/bin/dsymutil $out/bin/dsymutil
    '';
  };
in pkgs.symlinkJoin {
  name = "go-libnix-toolchain";
  passthru = { inherit pkgs go compile link native internalLink; };
  # Only discovery uses cmd/go and the complete SDK. The generated graph
  # substitutes worker tools and snapshots each selected standard package.
  paths = [ native go ];
  postBuild = ''
    mkdir -p $out/nix-support
    echo ${compile} > $out/nix-support/drvgo-compile-toolchain
    echo ${link} > $out/nix-support/drvgo-link-toolchain
    echo ${native} > $out/nix-support/drvgo-native-toolchain
    echo ${internalLink} > $out/nix-support/drvgo-internal-link-toolchain
  '';
}
