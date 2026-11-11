//go:build integration

package export

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestCompressedNativeSources(t *testing.T) {
	src := writeTestProject(t, map[string]string{
		"go.mod": "module example.com/compressednative\ngo 1.25\n",
		"main.go": `package main
// #include "answer.h"
import "C"
import "fmt"
func main() { fmt.Println(C.answer()) }
`,
		"answer.h": "/*" + strings.Repeat("native source fixture ", 600) + "*/\nstatic int answer(void) { return 42; }\n",
	})
	out := filepath.Join(t.TempDir(), "export")
	cgo := true
	g, err := Generate(Options{Dir: src, Out: out, Package: ".", Toolchain: testToolchain(t), CGOEnabled: &cgo})
	if err != nil {
		t.Fatal(err)
	}
	compressed := false
	for _, unit := range g.Units {
		if unit.Name == "example.com/compressednative" {
			compressed = strings.Contains(unit.Script, "drvgo-source-zlib")
		}
	}
	if !compressed {
		t.Fatal("native source fixture was not compressed")
	}
	if err := os.RemoveAll(src); err != nil {
		t.Fatal(err)
	}
	args := []string{out, "-A", "default", "--no-out-link", "--max-jobs", "4", "--cores", "2",
		"--option", "sandbox", "true", "--option", "sandbox-fallback", "false"}
	cmd := exec.Command("nix-build", args...)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	result, err := cmd.Output()
	if err != nil {
		t.Fatalf("compressed native build: %v\n%s", err, stderr.String())
	}
	binary := filepath.Join(strings.TrimSpace(string(result)), "bin", g.BinaryName)
	if b, err := exec.Command(binary).CombinedOutput(); err != nil || string(b) != "42\n" {
		t.Fatalf("compressed native run: %v: %s", err, b)
	}
	// Moving native headers into the work directory must remain reproducible,
	// including debug paths on platforms with different sandbox layouts.
	if b, err := exec.Command("nix-build", append(args, "--check")...).CombinedOutput(); err != nil {
		t.Fatalf("compressed native reproducibility: %v\n%s", err, b)
	}
}

func TestNestedCommandWithoutOriginalSource(t *testing.T) {
	root := t.TempDir()
	src := filepath.Join(root, "source")
	if err := os.MkdirAll(filepath.Join(src, "cmd", "hello"), 0755); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{
		"go.mod":            "module example.com/hello\n\ngo 1.25\n",
		"cmd/hello/main.go": "package main\nimport (\"fmt\"; _ \"net\")\nfunc main() { fmt.Println(\"hello\") }\n",
	} {
		if err := os.WriteFile(filepath.Join(src, name), []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	out := filepath.Join(root, "export")
	graph, err := Generate(Options{Dir: src, Out: out, Package: "./cmd/hello", Toolchain: testToolchain(t)})
	if err != nil {
		t.Fatal(err)
	}
	for _, unit := range graph.Units {
		if strings.Contains(unit.Script, "cd "+src+"\n") {
			t.Errorf("%s retains the original working directory", unit.Name)
		}
	}
	if t.Failed() {
		t.FailNow()
	}
	if err := os.RemoveAll(src); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("nix-build", out, "-A", "default", "--no-out-link",
		"--option", "sandbox", "true", "--option", "sandbox-fallback", "false",
		"--max-jobs", "4", "--cores", "2")
	var stderr strings.Builder
	cmd.Stderr = &stderr
	b, err := cmd.Output()
	if err != nil {
		t.Fatalf("sandbox build: %v\n%s", err, stderr.String())
	}
	b, err = exec.Command(filepath.Join(strings.TrimSpace(string(b)), "bin", "hello")).CombinedOutput()
	if err != nil || string(b) != "hello\n" {
		t.Fatalf("run: %v: %s", err, b)
	}
}

func TestNativeObjectWithoutOriginalSource(t *testing.T) {
	src := writeTestProject(t, map[string]string{
		"go.mod":  "module example.com/nativeobject\ngo 1.25\n",
		"main.go": "package main\nimport \"fmt\"\nfunc main(){fmt.Println(\"native object\")}\n",
	})
	tc := testToolchain(t)
	cc := "gcc"
	if runtime.GOOS == "darwin" {
		cc = "clang"
	}
	// Keep the C source outside the package: the target uses a prebuilt object
	// with cgo disabled, rather than importing C or compiling C source itself.
	root := t.TempDir()
	objectSource := filepath.Join(root, "unused.c")
	if err := os.WriteFile(objectSource, []byte("int unused_answer(void) { return 42; }\n"), 0644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(filepath.Join(tc, "bin", cc), "-c", objectSource, "-o", filepath.Join(src, "unused.syso"))
	cmd.Env = controlledEnv(tc)
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("compile native object: %v\n%s", err, b)
	}
	out := filepath.Join(root, "export")
	cgo := false
	g, err := Generate(Options{Dir: src, Out: out, Package: ".", Toolchain: tc, CGOEnabled: &cgo})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(src); err != nil {
		t.Fatal(err)
	}
	cmd = exec.Command("nix-build", out, "-A", "default", "--no-out-link",
		"--option", "sandbox", "true", "--option", "sandbox-fallback", "false",
		"--max-jobs", "4", "--cores", "2")
	var stderr strings.Builder
	cmd.Stderr = &stderr
	b, err := cmd.Output()
	if err != nil {
		t.Fatalf("sandbox build after source removal: %v\n%s", err, stderr.String())
	}
	b, err = exec.Command(filepath.Join(strings.TrimSpace(string(b)), "bin", g.BinaryName)).CombinedOutput()
	if err != nil || string(b) != "native object\n" {
		t.Fatalf("run: %v: %s", err, b)
	}
}
