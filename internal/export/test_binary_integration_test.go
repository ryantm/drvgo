//go:build integration

package export

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func testToolchain(t *testing.T) string {
	t.Helper()
	if tc := os.Getenv("DRVGO_TEST_TOOLCHAIN"); tc != "" {
		return tc
	}
	tc, err := Toolchain()
	if err != nil {
		t.Fatal(err)
	}
	return tc
}

func writeTestProject(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for name, text := range files {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(text), 0644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

func buildTestExport(t *testing.T, out string, g *Graph) string {
	t.Helper()
	cmd := exec.Command("nix-build", out, "-A", "default", "--no-out-link", "--cores", "2", "--max-jobs", "4")
	var stderr strings.Builder
	cmd.Stderr = &stderr
	result, err := cmd.Output()
	if err != nil {
		t.Fatalf("realize: %v\n%s", err, stderr.String())
	}
	return filepath.Join(strings.TrimSpace(string(result)), "bin", g.BinaryName)
}

func TestTestBinaryInternalExternalAndFailure(t *testing.T) {
	root := writeTestProject(t, map[string]string{
		"go.mod":   "module example.com/testbinary.test\n\ngo 1.25\n",
		"value.go": "package value\nfunc Value() int { return 42 }\n",
		"value_test.go": `package value
import ("testing"; "os"; "fmt"; _ "embed")
//go:embed fixture.txt
var fixture string
func TestMain(m *testing.M) { fmt.Println("custom TestMain"); os.Exit(m.Run()) }
func TestValue(t *testing.T) {
 if Value()!=42 || fixture!="fixture" { t.Fatal("bad value") }
 if data, err := os.ReadFile("runtime.txt"); err != nil || string(data)!="runtime fixture" { t.Fatal("runtime fixture", err) }
 if os.Getenv("DRVGO_FORCE_FAILURE")=="1" { t.Fatal("intentional failure") }
}
`,
		"external_test.go": `package value_test
import ("testing"; value "example.com/testbinary.test"; _ "embed")
//go:embed external.txt
var external string
func TestExternal(t *testing.T) { if value.Value()!=42 || external!="external fixture" { t.Fatal("bad external value") } }
`,
		"fixture.txt":  "fixture",
		"external.txt": "external fixture",
		"runtime.txt":  "runtime fixture",
	})
	out := filepath.Join(t.TempDir(), "export")
	cgo := false
	g, err := Generate(Options{Dir: root, Out: out, Package: ".", Toolchain: testToolchain(t), Test: true, CGOEnabled: &cgo})
	if err != nil {
		t.Fatal(err)
	}
	if g.CGO != "0" {
		t.Fatal("ignored cgo setting")
	}
	// Neither the original source path nor its runtime-only fixtures may be
	// needed to compile the standalone export. Run from the relocated package.
	moved := root + "-moved"
	if err := os.Rename(root, moved); err != nil {
		t.Fatal(err)
	}
	binary := buildTestExport(t, out, g)
	cmd := exec.Command(binary, "-test.v")
	cmd.Dir = moved
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("test run: %v\n%s", err, output)
	}
	for _, want := range []string{"custom TestMain", "PASS: TestExternal", "PASS: TestValue"} {
		if !strings.Contains(string(output), want) {
			t.Fatalf("missing %s: %s", want, output)
		}
	}
	cmd = exec.Command(binary)
	cmd.Dir = moved
	cmd.Env = append(os.Environ(), "DRVGO_FORCE_FAILURE=1")
	if output, err = cmd.CombinedOutput(); err == nil || !strings.Contains(string(output), "intentional failure") {
		t.Fatalf("failure not propagated: %v %s", err, output)
	}
}

func TestTestBinaryMainPackage(t *testing.T) {
	root := writeTestProject(t, map[string]string{
		"go.mod":       "module example.com/main.test\ngo 1.25\n",
		"main.go":      "package main\nfunc main() { panic(\"application main must not run\") }\n",
		"main_test.go": "package main\nimport \"testing\"\nfunc TestMainPackage(t *testing.T) {}\n",
	})
	out := filepath.Join(t.TempDir(), "export")
	g, err := Generate(Options{Dir: root, Out: out, Package: ".", Toolchain: testToolchain(t), Test: true})
	if err != nil {
		t.Fatal(err)
	}
	output, err := exec.Command(buildTestExport(t, out, g), "-test.v").CombinedOutput()
	if err != nil || !strings.Contains(string(output), "PASS: TestMainPackage") {
		t.Fatalf("test main: %v %s", err, output)
	}
}

func TestTestBinaryRaceAndCGO(t *testing.T) {
	root := writeTestProject(t, map[string]string{
		"go.mod":    "module example.com/racetest\ngo 1.25\n",
		"native.go": "package value\n/* static int answer(void) { return 42; } */\nimport \"C\"\nfunc Value() int { return int(C.answer()) }\n",
		"value_test.go": `package value
import ("testing"; "os")
var shared int
func TestNative(t *testing.T) { if Value()!=42 { t.Fatal("cgo value") } }
func TestRace(t *testing.T) {
 if os.Getenv("DRVGO_FORCE_RACE")!="1" { return }
 done := make(chan struct{})
 go func() { shared++; close(done) }()
 shared++
 <-done
}
`,
	})
	out := filepath.Join(t.TempDir(), "export")
	cgo := true
	g, err := Generate(Options{Dir: root, Out: out, Package: ".", Toolchain: testToolchain(t), Test: true, Race: true, CGOEnabled: &cgo})
	if err != nil {
		t.Fatal(err)
	}
	if g.CGO != "1" {
		t.Fatal("ignored enabled cgo setting")
	}
	binary := buildTestExport(t, out, g)
	output, err := exec.Command(binary, "-test.v").CombinedOutput()
	if err != nil || !strings.Contains(string(output), "PASS: TestNative") {
		t.Fatalf("race-enabled cgo test: %v %s", err, output)
	}
	cmd := exec.Command(binary, "-test.run=TestRace")
	cmd.Env = append(os.Environ(), "DRVGO_FORCE_RACE=1", "GORACE=halt_on_error=1")
	output, err = cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(output), "WARNING: DATA RACE") {
		t.Fatalf("race detector inactive: %v %s", err, output)
	}
}

func TestTestBinaryRejectsPackageWithoutTests(t *testing.T) {
	root := writeTestProject(t, map[string]string{
		"go.mod":   "module example.com/empty\ngo 1.25\n",
		"empty.go": "package empty\n",
	})
	out := filepath.Join(t.TempDir(), "export")
	_, err := Generate(Options{Dir: root, Out: out, Package: ".", Toolchain: testToolchain(t), Test: true})
	if err == nil || !strings.Contains(err.Error(), "no generated test main") {
		t.Fatalf("empty package: %v", err)
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Fatal("failed export left output behind")
	}
}
