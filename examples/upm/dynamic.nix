{ source ? builtins.fetchTree {
    type = "github";
    owner = "replit";
    repo = "upm";
    rev = "35a76c282507fb942445c0ee98427b3d1ef772bc";
  }
}:
let
  build = import ../../nix/dynamic.nix {};
  common = {
    src = source;
    vendorHash = "sha256-A4CU4C5SmEZP6Q+CVHjp+Jy4UxRXAnpvhzrsdq4NlsM=";
  };
  command = build (common // { name = "upm"; package = "./cmd/upm"; });
  generator = build (common // {
    name = "upm-generator";
    package = "./internal/backends/python/gen_pypi_map";
  });
in import ./package.nix { inherit source command generator; }
