{ pkgs, go ? pkgs.go }:
let
  # Nixpkgs ships the Darwin resolver separately from the SDK. Our builders
  # do not source stdenv setup, so make its search path part of the compiler
  # wrapper itself, for both planning and replay (including Go's net package).
  cc = if pkgs.stdenv.hostPlatform.isDarwin then
    pkgs.stdenv.cc.override (old: {
      extraBuildCommands = (old.extraBuildCommands or "") + ''
        echo '-L${pkgs.lib.getLib pkgs.darwin.libresolv}/lib' >> $out/nix-support/cc-ldflags
      '';
    })
  else pkgs.stdenv.cc;
  helpers = pkgs.runCommand "go-libnix-helpers" { nativeBuildInputs = [ go ]; } ''
    export HOME=$TMPDIR GOCACHE=$TMPDIR/cache GOTOOLCHAIN=local
    mkdir -p $out/bin
    go build -trimpath -o $out/bin/libnix-buildid cmd/buildid
    go build -trimpath -o $out/bin/libnix-pack cmd/pack
  '';
  # Most package actions only invoke Go tools. Keep the C compiler and its development
  # closure off those builders, particularly when copying inputs remotely.
  compile = pkgs.symlinkJoin {
    name = "go-libnix-compile-toolchain";
    paths = [ helpers go pkgs.bash pkgs.coreutils pkgs.gnused ];
  };
in pkgs.symlinkJoin {
  name = "go-libnix-toolchain";
  passthru = { inherit pkgs go compile; };
  paths = [ compile cc pkgs.findutils pkgs.pkg-config ];
  postBuild = ''
    mkdir -p $out/nix-support
    echo ${compile} > $out/nix-support/drvgo-compile-toolchain
  '' + pkgs.lib.optionalString pkgs.stdenv.hostPlatform.isDarwin ''
    # Go invokes these directly after external linking. symlinkJoin does not
    # follow the compiler's propagated user-environment packages.
    for tool in dsymutil strip; do
      ln -s ${pkgs.lib.getBin pkgs.stdenv.cc.bintools.bintools}/bin/$tool $out/bin/$tool
    done
  '';
}
