# Supply a built iets store output. With nested=true, use its classic evaluator
# inside the recursive planner as well as using iets for the outer build.
{ iets, src ? ./fixture, name ? "drvgo-iets-fixture", nested ? false
, cgoEnabled ? null, test ? false, allowEmptyTests ? false, tags ? ""
, runtimeInputs ? [], ldflags ? ""
}:
let
  tc = import ../internal/export/templates/toolchain.nix;
  pkgs = tc.pkgs;
  instantiate = pkgs.writeShellScriptBin "nix-instantiate" ''
    export IETS_NIX_FALLBACK=never IETS_PROGRESS=false IETS_WORKERS=1 IETS_CACHE=false
    # PR #3 reads NIX_DAEMON_SOCKET_PATH rather than NIX_REMOTE. Its classic
    # instantiator requires the local-daemon transport, even for a Unix socket.
    case "$NIX_REMOTE" in
      unix://*) export NIX_DAEMON_SOCKET_PATH="''${NIX_REMOTE#unix://}" ;;
      *) echo "expected a recursive Nix Unix socket, got $NIX_REMOTE" >&2; exit 1 ;;
    esac
    exec -a nix-instantiate ${iets}/bin/iets "$@"
  '';
  commands = pkgs.runCommand "drvgo-iets-commands" {} ''
    mkdir -p $out/bin
    ln -s ${instantiate}/bin/nix-instantiate $out/bin/nix-instantiate
    ln -s ${pkgs.nix}/bin/nix-store $out/bin/nix-store
    ln -s ${pkgs.nix}/bin/nix $out/bin/nix
  '';
  toolchain = if nested then tc // { pkgs = pkgs // { nix = commands; }; } else tc;
in (import ../nix/dynamic.nix { inherit toolchain; }) {
  inherit src name cgoEnabled test allowEmptyTests tags runtimeInputs ldflags;
}
