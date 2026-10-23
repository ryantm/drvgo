{ source ? ./work/upm
, command ? (import (source + "/.drvgo") {}).default
, generator ? (import (source + "/.drvgo-generator") {}).default
}:
let
  toolchain = import ../../internal/export/templates/toolchain.nix;
  pkgs = toolchain.pkgs;
  database = pkgs.runCommand "upm-pypi-map" {} ''
    mkdir -p $out
    ${generator}/bin/gen_pypi_map gen \
      -out "$out/pypi_map.sqlite" \
      -pkgsfile ${source + "/internal/backends/python/pkgs.json"} \
      -legacypkgsfile ${source + "/internal/backends/python/pypi_packages.json"} \
      -bq ${source + "/internal/backends/python/download_stats.json"}
    # The upstream generator can report errors without a failing exit status.
    test -s "$out/pypi_map.sqlite"
  '';
  package = pkgs.runCommand "upm-drvgo" { nativeBuildInputs = [ pkgs.makeWrapper ]; } ''
    mkdir -p $out/bin
    ln -s ${database}/pypi_map.sqlite $out/pypi_map.sqlite
    makeWrapper ${command}/bin/upm $out/bin/upm \
      --set PYPI_MAP_DB ${database}/pypi_map.sqlite
  '';
in { inherit command generator database package; }
