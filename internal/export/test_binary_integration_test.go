//go:build integration

package export

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestTestBinaryInternalExternalAndFailure(t *testing.T) {
	tc := os.Getenv("DRVGO_TEST_TOOLCHAIN")
	if tc == "" {
		t.Skip("set DRVGO_TEST_TOOLCHAIN to a built toolchain")
	}
	root := t.TempDir()
	files := map[string]string{
		"go.mod":           "module example.com/testbinary\n\ngo 1.25\n",
		"value.go":         "package value\nfunc Value() int { return 42 }\n",
		"value_test.go":    "package value\nimport(\"testing\";\"os\"; _ \"embed\")\n//go:embed fixture.txt\nvar fixture string\nfunc TestValue(t *testing.T) { if Value()!=42 || fixture!=\"fixture\" { t.Fatal(\"bad value\") }; if os.Getenv(\"DRVGO_FORCE_FAILURE\")==\"1\" { t.Fatal(\"intentional failure\") } }\n",
		"external_test.go": "package value_test\nimport(\"testing\"; value \"example.com/testbinary\")\nfunc TestExternal(t *testing.T) { if value.Value()!=42 { t.Fatal(\"bad value\") } }\n",
		"fixture.txt":      "fixture",
	}
	for name, text := range files {
		if err := os.WriteFile(filepath.Join(root, name), []byte(text), 0644); err != nil {
			t.Fatal(err)
		}
	}
	out := filepath.Join(t.TempDir(), "export")
	cgo := false
	g, err := Generate(Options{Dir: root, Out: out, Package: ".", Toolchain: tc, ExperimentalGo: true, Test: true, CGOEnabled: &cgo})
	if err != nil {
		t.Fatal(err)
	}
	if g.CGO != "0" {
		t.Fatal("ignored cgo setting")
	}
	cmd := exec.Command("nix-build", out, "--no-out-link", "--cores", "2", "--max-jobs", "4")
	var stderr strings.Builder
	cmd.Stderr = &stderr
	result, err := cmd.Output()
	if err != nil {
		t.Fatalf("realize: %v\n%s", err, stderr.String())
	}
	binary := filepath.Join(strings.TrimSpace(string(result)), "bin", g.BinaryName)
	cmd = exec.Command(binary, "-test.v")
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("test run: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "PASS: TestExternal") || !strings.Contains(string(output), "PASS: TestValue") {
		t.Fatalf("tests missing: %s", output)
	}
	cmd = exec.Command(binary)
	cmd.Env = append(os.Environ(), "DRVGO_FORCE_FAILURE=1")
	if output, err = cmd.CombinedOutput(); err == nil || !strings.Contains(string(output), "intentional failure") {
		t.Fatalf("failure not propagated: %v %s", err, output)
	}
}
