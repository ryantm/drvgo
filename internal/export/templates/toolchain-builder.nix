{ pkgs, go ? pkgs.go
, runnerSource ? builtins.toFile "drvgo-builder.go" (builtins.readFile ./runner.go)
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
  helpers = pkgs.runCommand "go-libnix-helpers" { nativeBuildInputs = [ go ]; } ''
    export HOME=$TMPDIR GOCACHE=$TMPDIR/cache GOTOOLCHAIN=local CGO_ENABLED=0
    mkdir -p $out/bin
    go build -trimpath -ldflags='-s -w' -o $out/bin/libnix-buildid cmd/buildid
    go build -trimpath -ldflags='-s -w' -o $out/bin/libnix-pack cmd/pack
    go build -trimpath -ldflags='-s -w' -o $out/bin/drvgo-builder ${runnerSource}
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
    paths = [ link (tools [ "cgo" ]) cc pkgs.bash pkgs.coreutils pkgs.gnused pkgs.findutils pkgs.pkg-config ];
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
