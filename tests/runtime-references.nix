# Exercise references from selected source, embedded data and linker flags.
# explicit=true models literal paths in an existing checkout, without context.
{ toolchain ? import ../internal/export/templates/toolchain.nix, explicit ? false }:
let
  pkgs = toolchain.pkgs;
  sourceData = builtins.toFile "drvgo-source-data" "source data";
  embeddedData = builtins.toFile "drvgo-embedded-data" "embedded data";
  linkedData = builtins.toFile "drvgo-linked-data" "linked data";
  unusedData = builtins.toFile "drvgo-unused-data" "unused data";
  literal = path: if explicit then builtins.unsafeDiscardStringContext path else path;
  main = builtins.toFile "main.go" ''
    package main
    import ("fmt"; "os"; _ "embed")
    //go:embed path.txt
    var embedded string
    var linked string
    func main() {
      for _, path := range []string{"${literal sourceData}", embedded, linked} {
        b, err := os.ReadFile(path)
        if err != nil { panic(err) }
        fmt.Println(string(b))
      }
    }
  '';
  asset = builtins.toFile "path.txt" (literal embeddedData);
  excluded = builtins.toFile "excluded.go" ''
    //go:build drvgo_unused
    package main
    const unused = "${literal unusedData}"
  '';
  mod = builtins.toFile "go.mod" "module example.com/runtime-reference\ngo 1.25\n";
in {
  src = pkgs.runCommand "drvgo-runtime-source" {} ''
    mkdir -p $out
    cp ${main} $out/main.go
    cp ${asset} $out/path.txt
    cp ${excluded} $out/excluded.go
    cp ${mod} $out/go.mod
  '';
  ldflags = "-X main.linked=${literal linkedData}";
  runtimeInputs = [ unusedData ] ++ pkgs.lib.optionals explicit [ sourceData embeddedData linkedData ];
  references = [ sourceData embeddedData linkedData ];
  inherit unusedData;
}
