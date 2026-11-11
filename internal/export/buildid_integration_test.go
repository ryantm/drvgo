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
