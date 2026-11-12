package export

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/ryantm/drvgo/internal/plan"
)

func TestNixScriptsPreserveTextAndContext(t *testing.T) {
	nix, err := exec.LookPath("nix-instantiate")
	if err != nil {
		t.Skip("nix-instantiate unavailable")
	}
	root := t.TempDir()
	script := "echo \"${shell}\" '\\n'\n\t\ré雪 @DEP:unknown@DEP:b1@ @SRC:01abc@ @WORK@\n"
	units := []plan.Unit{{ID: "b0", Script: script, Deps: []string{"b1"}, Sources: []string{"01abc"}}}
	file := filepath.Join(root, "scripts.nix")
	if err := os.WriteFile(file, []byte(nixScripts(units)), 0644); err != nil {
		t.Fatal(err)
	}
	// Compare both the bytes and Nix string context to the old implementation.
	expr := `let
   dep = builtins.toFile "drvgo-script-dependency" "dependency";
   src = builtins.toFile "drvgo-script-source" "source";
   actual = (import ` + file + ` { b1 = dep; } { "01abc" = src; }).b0;
   expected = builtins.replaceStrings [ "@DEP:b1@" "@SRC:01abc@" ] [ dep src ] ` + nixString(script) + `;
 in { text = actual == expected; context = builtins.getContext actual == builtins.getContext expected; }`
	out, err := exec.Command(nix, "--eval", "--strict", "--json", "--expr", expr).CombinedOutput()
	if err != nil {
		t.Fatalf("eval: %v\n%s", err, out)
	}
	var result map[string]bool
	if err := json.Unmarshal(out, &result); err != nil {
		t.Fatal(err)
	}
	if !result["text"] || !result["context"] {
		t.Fatalf("rendered replay differs: %s", out)
	}
}

func TestNixScriptsSelectCompileInterface(t *testing.T) {
	nix, err := exec.LookPath("nix-instantiate")
	if err != nil {
		t.Skip("nix-instantiate unavailable")
	}
	file := filepath.Join(t.TempDir(), "scripts.nix")
	units := []plan.Unit{
		{ID: "compile", Script: "@DEP:dep@/_pkg_.a", Deps: []string{"dep"}},
		{ID: "link", Script: "@DEP:dep@/_pkg_.a", Deps: []string{"dep"}, Binary: true},
	}
	if err := os.WriteFile(file, []byte(nixScripts(units)), 0644); err != nil {
		t.Fatal(err)
	}
	expr := `let
  full = builtins.toFile "drvgo-full-archive" "full";
  api = builtins.toFile "drvgo-export-archive" "export data";
  scripts = import ` + file + ` { dep = { outPath = full; inherit api; }; } {};
in assert scripts.compile == "${api}/_pkg_.a";
   assert scripts.link == "${full}/_pkg_.a";
   assert builtins.getContext scripts.compile == builtins.getContext api;
   assert builtins.getContext scripts.link == builtins.getContext full;
   true`
	if b, err := exec.Command(nix, "--eval", "--strict", "--expr", expr).CombinedOutput(); err != nil {
		t.Fatalf("interface context: %s (%v)", b, err)
	}
}
