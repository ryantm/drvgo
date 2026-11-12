//go:build integration

package export

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestEmptyTestResult(t *testing.T) {
	root := writeTestProject(t, map[string]string{
		"go.mod":         "module example.com/emptyresult\ngo 1.25\n",
		"sample.go":      "package sample\n",
		"sample_test.go": "//go:build special\n\npackage sample_test\nimport \"testing\"\nfunc TestA(t *testing.T){}\n",
	})
	out := filepath.Join(t.TempDir(), "export")
	tc := testToolchain(t)
	g, err := Generate(Options{Dir: root, Out: out, Package: ".", Toolchain: tc, Test: true, AllowEmptyTests: true})
	if err != nil {
		t.Fatal(err)
	}
	if !g.EmptyTests {
		t.Fatal("expected explicit empty result")
	}
	if g.Driver == "" || g.GoOS == "" || g.GoArch == "" || g.GoRoot == "" || g.CGO == "" {
		t.Fatalf("empty result lost toolchain metadata: %+v", g)
	}
	cmd := exec.Command("nix-build", out, "-A", "default", "--no-out-link")
	b, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	result := strings.TrimSpace(string(b))
	marker, err := os.ReadFile(filepath.Join(result, "nix-support", "no-tests"))
	if err != nil || string(marker) != "{\"version\":1,\"status\":\"no-tests\"}\n" {
		t.Fatalf("marker: %q %v", marker, err)
	}
	if _, err := os.Stat(filepath.Join(result, "bin")); !os.IsNotExist(err) {
		t.Fatalf("empty result has bin: %v", err)
	}
	// A portable export must contain all sources needed to recreate its default
	// toolchain, even while the selected toolchain happens to exist locally.
	if b, err := exec.Command("nix-instantiate", "--eval", "--strict", "--expr", "(import "+out+"/toolchain.nix).drvPath").CombinedOutput(); err != nil {
		t.Fatalf("portable empty-test toolchain: %s (%v)", b, err)
	}
	g, err = Generate(Options{Dir: root, Out: out, Package: ".", Toolchain: tc, Test: true, AllowEmptyTests: true, Tags: "'special'"})
	if err != nil {
		t.Fatal(err)
	}
	if g.EmptyTests {
		t.Fatal("enabled test was skipped")
	}
	binary := buildTestExport(t, out, g)
	if b, err := exec.Command(binary, "-test.v").CombinedOutput(); err != nil || !strings.Contains(string(b), "PASS: TestA") {
		t.Fatalf("enabled test: %s %v", b, err)
	}
}
