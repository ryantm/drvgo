//go:build integration

package export

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRuntimeStoreReferences(t *testing.T) {
	b, err := exec.Command("nix-instantiate", "--eval", "--strict", "--read-write-mode", "--json", "--expr",
		`builtins.toFile "drvgo-runtime-data" "runtime data"`).CombinedOutput()
	if err != nil {
		t.Fatalf("runtime data: %v\n%s", err, b)
	}
	var dataPath string
	if err := json.Unmarshal(b, &dataPath); err != nil {
		t.Fatal(err)
	}
	if b, err := exec.Command("nix-store", "--check-validity", dataPath).CombinedOutput(); err != nil {
		t.Fatalf("runtime data is not registered: %v\n%s", err, b)
	}
	tc := testToolchain(t)
	for _, padding := range []int{0, inlineSourceLimit * 2} {
		t.Run(fmt.Sprintf("padding-%d", padding), func(t *testing.T) {
			root := writeTestProject(t, map[string]string{
				"go.mod": "module example.com/runtime-reference\ngo 1.25\n",
				"main.go": fmt.Sprintf("package main\nimport(\"fmt\";\"os\")\nfunc main(){ b,e:=os.ReadFile(%q);if e!=nil{panic(e)};fmt.Print(string(b)) }\n// /nix/store/00000000000000000000000000000000-missing-example\n// %s\n",
					dataPath, strings.Repeat("x", padding)),
			})
			out := filepath.Join(t.TempDir(), "export")
			if _, err := Generate(Options{Dir: root, Out: out, Package: ".", Toolchain: tc}); err != nil {
				t.Fatal(err)
			}
			if err := os.RemoveAll(root); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command("nix-build", out, "-A", "default", "--no-out-link", "--max-jobs", "2", "--cores", "2",
				"--option", "allow-import-from-derivation", "false",
				"--option", "sandbox", "true", "--option", "sandbox-fallback", "false")
			var stderr strings.Builder
			cmd.Stderr = &stderr
			b, err := cmd.Output()
			if err != nil {
				t.Fatalf("build: %v\n%s", err, stderr.String())
			}
			result := strings.TrimSpace(string(b))
			b, err = exec.Command("nix-store", "--query", "--references", result).CombinedOutput()
			if err != nil {
				t.Fatalf("references: %v\n%s", err, b)
			}
			if !strings.Contains("\n"+string(b), "\n"+dataPath+"\n") {
				t.Fatalf("runtime data %s missing from output references:\n%s", dataPath, b)
			}
			goBinary, err := filepath.EvalSymlinks(filepath.Join(tc, "bin", "go"))
			if err != nil {
				t.Fatal(err)
			}
			sdk := storeReference.FindString(goBinary)
			closure, err := exec.Command("nix-store", "--query", "--requisites", result).Output()
			if err != nil || sdk == "" {
				t.Fatalf("SDK closure check: %v, SDK %q", err, sdk)
			}
			if strings.Contains("\n"+string(closure), "\n"+sdk+"\n") {
				t.Fatal("runtime closure retained the full Go SDK")
			}
			b, err = exec.Command(filepath.Join(result, "bin", "runtime-reference")).CombinedOutput()
			if err != nil || string(b) != "runtime data" {
				t.Fatalf("runtime read: %q %v", b, err)
			}
			// Copy the complete closure to a fresh binary cache and verify the
			// runtime data travels with it, without collecting the host store.
			cache := "file://" + t.TempDir()
			b, err = exec.Command("nix", "--extra-experimental-features", "nix-command", "copy", "--to", cache, result).CombinedOutput()
			if err != nil {
				t.Fatalf("copy closure: %v\n%s", err, b)
			}
			b, err = exec.Command("nix", "--extra-experimental-features", "nix-command", "path-info", "--store", cache, dataPath).CombinedOutput()
			if err != nil {
				t.Fatalf("runtime data missing from copied closure: %v\n%s", err, b)
			}
		})
	}
}
