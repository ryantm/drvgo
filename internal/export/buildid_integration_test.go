//go:build integration

package export

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestCanonicalBuildIDRelocation(t *testing.T) {
	files := map[string]string{
		"go.mod": "module example.com/buildidentity\ngo 1.25\n",
		"main.go": `package main
/*
#cgo CFLAGS: -I.
#include "root.h"
*/
import "C"
import ("fmt"; "example.com/buildidentity/native")
func main() { fmt.Println(int(C.rootAnswer()) + native.Value()) }
`,
		"root.h": "static int rootAnswer(void) { return 20; }\n",
		"native/native.go": `package native
/*
#cgo CFLAGS: -I.
#include "value.h"
*/
import "C"
func Value() int { return int(C.answer()); }
`,
		"native/value.h": "static int answer(void) { return 22; }\n",
	}
	tc := testToolchain(t)
	paths := func(out string, g *Graph) map[string]string {
		t.Helper()
		cmd := exec.Command("nix-instantiate", "--eval", "--strict", "--json", "--expr",
			"builtins.mapAttrs (_: d: d.drvPath) (import "+out+" {}).units")
		data, err := cmd.Output()
		if err != nil {
			t.Fatal(err)
		}
		byID := map[string]string{}
		if err := json.Unmarshal(data, &byID); err != nil {
			t.Fatal(err)
		}
		byName := map[string]string{}
		for _, u := range g.Units {
			byName[u.Name] = byID[u.ID]
		}
		return byName
	}
	for _, mode := range []struct{ name, flags, customID string }{
		{"automatic", "", ""}, {"custom", "-buildid=kept/ID", "kept/ID"}, {"empty", "-buildid=", ""},
	} {
		t.Run(mode.name, func(t *testing.T) {
			first, second := writeTestProject(t, files), writeTestProject(t, files)
			generate := func(source, name string) (string, *Graph) {
				t.Helper()
				out := filepath.Join(t.TempDir(), name)
				g, err := Generate(Options{Dir: source, Out: out, Package: ".", Toolchain: tc, LDFlags: mode.flags})
				if err != nil {
					t.Fatal(err)
				}
				return out, g
			}
			out1, g1 := generate(first, "first")
			out2, g2 := generate(second, "second")
			before := paths(out1, g1)
			if !reflect.DeepEqual(before, paths(out2, g2)) {
				t.Fatal("relocating identical cgo sources changed package recipes")
			}
			if err := os.RemoveAll(first); err != nil {
				t.Fatal(err)
			}
			binary := buildTestExport(t, out1, g1)
			if other := buildTestExport(t, out2, g2); binary != other {
				t.Fatal("relocation missed the output cache")
			}
			if b, err := exec.Command(binary).CombinedOutput(); err != nil || string(b) != "42\n" {
				t.Fatalf("run: %s (%v)", b, err)
			}
			id, err := exec.Command(tc+"/bin/go", "tool", "buildid", binary).Output()
			if err != nil {
				t.Fatal(err)
			}
			if mode.flags != "" {
				if strings.TrimSpace(string(id)) != mode.customID {
					t.Fatalf("custom ID changed: %q", id)
				}
				return
			}
			parts := strings.Split(strings.TrimSpace(string(id)), "/")
			if len(parts) != 4 || parts[1] == parts[2] {
				t.Fatalf("expected actual main archive content ID: %q", id)
			}
			// Adding an unrelated import renumbers cmd/go's actions. Hashing
			// unresolved @DEP:bNNN@ tokens would invalidate shared dependencies.
			withImport := strings.Replace(files["main.go"], `"fmt";`, `"fmt"; _ "encoding/json";`, 1)
			if err := os.WriteFile(filepath.Join(second, "main.go"), []byte(withImport), 0644); err != nil {
				t.Fatal(err)
			}
			imported, ig := generate(second, "imported")
			afterImport := paths(imported, ig)
			for name, path := range before {
				if name != "example.com/buildidentity" && afterImport[name] != path {
					t.Fatalf("new import changed unrelated package %s", name)
				}
			}
			before = afterImport
			if err := os.WriteFile(filepath.Join(second, "native/value.h"), []byte("static int answer(void) { return 23; }\n"), 0644); err != nil {
				t.Fatal(err)
			}
			edited, eg := generate(second, "edited")
			changed := map[string]bool{}
			for name, path := range paths(edited, eg) {
				if path != before[name] {
					changed[name] = true
				}
			}
			if !reflect.DeepEqual(changed, map[string]bool{"example.com/buildidentity": true, "example.com/buildidentity/native": true}) {
				t.Fatalf("header edit changed %v", changed)
			}
			if b, err := exec.Command(buildTestExport(t, edited, eg)).CombinedOutput(); err != nil || string(b) != "43\n" {
				t.Fatalf("header edit missing: %s (%v)", b, err)
			}
		})
	}
}

func TestCustomBuildID(t *testing.T) {
	root := writeTestProject(t, map[string]string{
		"go.mod":  "module example.com/customid\n\ngo 1.25\n",
		"main.go": "package main\nfunc main() { println(\"custom ID works\") }\n",
	})
	tc := testToolchain(t)
	cgo := false
	for _, test := range []struct{ name, flags, id string }{
		{"redacted", "-buildid=redacted", "redacted"},
		{"empty", "-buildid=", ""},
		{"slash", "-buildid=custom/ID", "custom/ID"},
		{"spaces", `-buildid "custom ID"`, "custom ID"},
		{"double-dash", "--buildid=redacted", "redacted"},
		{"double-dash-spaces", `--buildid "custom ID"`, "custom ID"},
		{"last-wins", "-buildid=first --buildid=last", "last"},
		{"flag-like-value", "-buildid=custom/ID -r -buildid=path", "custom/ID"},
	} {
		t.Run(test.name, func(t *testing.T) {
			out := filepath.Join(t.TempDir(), "export")
			g, err := Generate(Options{Dir: root, Out: out, Package: ".", Toolchain: tc, CGOEnabled: &cgo, LDFlags: test.flags})
			if err != nil {
				t.Fatal(err)
			}
			binary := buildTestExport(t, out, g)
			b, err := exec.Command(tc+"/bin/go", "tool", "buildid", binary).CombinedOutput()
			if err != nil || strings.TrimSuffix(string(b), "\n") != test.id {
				t.Fatalf("build ID: got %q, want %q: %v", b, test.id, err)
			}
			b, err = exec.Command(binary).CombinedOutput()
			if err != nil || string(b) != "custom ID works\n" {
				t.Fatalf("run: %s: %v", b, err)
			}
		})
	}
}
