//go:build integration

package export

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCustomBuildID(t *testing.T) {
	root := writeTestProject(t, map[string]string{
		"go.mod":  "module example.com/customid\n\ngo 1.25\n",
		"main.go": "package main\nfunc main() { println(\"custom ID works\") }\n",
	})
	tc := testToolchain(t)
	cgo := false
	for _, id := range []string{"redacted", "", "custom/ID"} {
		t.Run(id, func(t *testing.T) {
			out := filepath.Join(t.TempDir(), "export")
			g, err := Generate(Options{Dir: root, Out: out, Package: ".", Toolchain: tc, CGOEnabled: &cgo, LDFlags: "-buildid=" + id})
			if err != nil {
				t.Fatal(err)
			}
			binary := buildTestExport(t, out, g)
			b, err := exec.Command(tc+"/bin/go", "tool", "buildid", binary).CombinedOutput()
			if err != nil || strings.TrimSpace(string(b)) != id {
				t.Fatalf("build ID: got %q, want %q: %v", b, id, err)
			}
			b, err = exec.Command(binary).CombinedOutput()
			if err != nil || string(b) != "custom ID works\n" {
				t.Fatalf("run: %s: %v", b, err)
			}
		})
	}
}
