# Unpack once per fixed proxy, rather than once per source edit. Content addressing
# keeps the cache's path stable when equivalent preparation recipes change.
{ pkgs, go, drvgo, modules }:
builtins.derivation {
  name = "drvgo-module-cache";
  system = pkgs.stdenv.hostPlatform.system;
  builder = "${drvgo}/bin/drvgo";
  args = [ "internal-prepare-module-cache" "${go}/bin/go" modules (builtins.placeholder "out") ];
  __contentAddressed = true;
  outputHashMode = "recursive";
  outputHashAlgo = "sha256";
}
