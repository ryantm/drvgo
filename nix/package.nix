{ pkgs }:
pkgs.buildGoModule {
  pname = "drvgo";
  version = "0.1.0";
  src = pkgs.lib.fileset.toSource {
    root = ../.;
    fileset = pkgs.lib.fileset.unions [ ../go.mod ../cmd ../internal ];
  };
  vendorHash = null;
  subPackages = [ "cmd/drvgo" ];
  # The packaged CLI is stripped. Skip generating compiler DWARF that will
  # be discarded; GOFLAGS also lets tests reuse the same compiled packages.
  GOFLAGS = [ "-gcflags=all=-dwarf=false" ];
  # Test library packages too, rather than only the CLI subpackage.
  checkPhase = ''
    runHook preCheck
    go test ./...
    go vet ./...
    runHook postCheck
  '';
  meta = {
    description = "Package-granular Go builds with Nix";
    homepage = "https://github.com/ryantm/drvgo";
    license = pkgs.lib.licenses.cc0;
    platforms = [ "x86_64-linux" "aarch64-linux" "aarch64-darwin" ];
    mainProgram = "drvgo";
  };
}
