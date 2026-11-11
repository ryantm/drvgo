//go:build integration

package export

import (
	"bytes"
	"debug/macho"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func TestCompressedNativeSources(t *testing.T) {
	for _, padding := range []struct {
		name        string
		repetitions int
	}{{"raw", 0}, {"compressed", 600}} {
		t.Run(padding.name, func(t *testing.T) {
			src := writeTestProject(t, map[string]string{
				"go.mod": "module example.com/compressednative\ngo 1.25\n",
				"main.go": `package main
// #include "answer.h"
import "C"
import "fmt"
func main() { fmt.Println(C.answer()) }
`,
				"answer.h": "/*" + strings.Repeat("native source fixture ", padding.repetitions) + "*/\nstatic int answer(void) { return 42; }\n",
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
			if compressed != (padding.repetitions != 0) {
				t.Fatalf("native source compressed = %v", compressed)
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
			checkNativeTemporaryPaths(t, out, g.BinaryName)
			// Moving native headers into the work directory must remain reproducible,
			// including debug paths on platforms with different sandbox layouts.
			if b, err := exec.Command("nix-build", append(args, "--check", "--keep-failed")...).CombinedOutput(); err != nil {
				logBinaryDifferences(t, binary, filepath.Join(strings.TrimSpace(string(result))+".check", "bin", g.BinaryName))
				t.Fatalf("compressed native reproducibility: %v\n%s", err, b)
			}
		})
	}
}

// Linux's Nix sandbox consistently uses /build, hiding accidental native debug
// references to TMPDIR. Replay the same root with its already-built dependencies
// in two different directories, including a symlink like macOS's /var alias.
func checkNativeTemporaryPaths(t *testing.T, graph, name string) {
	t.Helper()
	expression := `let d = (import ` + graph + ` {}).default; in {
		inherit (d) builder script PATH GOROOT GOOS GOARCH CGO_ENABLED GOENV
			GOTOOLCHAIN isBinary binaryName toolchainInput;
	}`
	b, err := exec.Command("nix-instantiate", "--eval", "--strict", "--json", "--expr", expression).Output()
	if err != nil {
		t.Fatal(err)
	}
	var env map[string]string
	if err := json.Unmarshal(b, &env); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	script := filepath.Join(root, "script")
	if err := os.WriteFile(script, []byte(env["script"]), 0644); err != nil {
		t.Fatal(err)
	}
	builder := env["builder"]
	delete(env, "builder")
	delete(env, "script")
	var first []byte
	for _, label := range []string{"first", "second"} {
		tmp := filepath.Join(root, label)
		if err := os.Mkdir(tmp, 0755); err != nil {
			t.Fatal(err)
		}
		if label == "second" {
			alias := tmp + "-alias"
			if err := os.Symlink(tmp, alias); err != nil {
				t.Fatal(err)
			}
			tmp = alias
		}
		out := filepath.Join(root, label+"-out")
		cmd := exec.Command(builder)
		for key, value := range env {
			cmd.Env = append(cmd.Env, key+"="+value)
		}
		cmd.Env = append(cmd.Env, "TMPDIR="+tmp, "NIX_BUILD_TOP="+tmp, "out="+out, "scriptPath="+script, "NIX_BUILD_CORES=2")
		if b, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("native replay: %v\n%s", err, b)
		}
		binary := filepath.Join(out, "bin", name)
		b, err := os.ReadFile(binary)
		if err != nil {
			t.Fatal(err)
		}
		if first == nil {
			first = b
		} else if !bytes.Equal(first, b) {
			logBinaryDifferences(t, filepath.Join(root, "first-out", "bin", name), binary)
			t.Fatal("native output depends on the temporary directory")
		}
	}
}

// Keep reproducibility failures actionable on remote workers: identify the
// differing Mach-O sections and symbols before the ephemeral worker disappears.
func logBinaryDifferences(t *testing.T, before, after string) {
	t.Helper()
	a, err := os.ReadFile(before)
	if err != nil {
		t.Logf("read original binary: %v", err)
		return
	}
	b, err := os.ReadFile(after)
	if err != nil {
		t.Logf("read rebuilt binary: %v", err)
		return
	}
	t.Logf("binary sizes: %d -> %d", len(a), len(b))
	logBytes := func(name string, a, b []byte) {
		if bytes.Equal(a, b) {
			return
		}
		first := 0
		for first < len(a) && first < len(b) && a[first] == b[first] {
			first++
		}
		start := max(0, first-32)
		t.Logf("%s: sizes %d -> %d, first difference at %#x: %q -> %q", name, len(a), len(b), first,
			a[start:min(len(a), first+96)], b[start:min(len(b), first+96)])
	}
	logBytes("file", a, b)
	ma, err := macho.NewFile(bytes.NewReader(a))
	if err != nil {
		return
	}
	mb, err := macho.NewFile(bytes.NewReader(b))
	if err != nil {
		return
	}
	for i, load := range ma.Loads {
		if i < len(mb.Loads) {
			logBytes("load command", load.Raw(), mb.Loads[i].Raw())
		}
	}
	for _, section := range ma.Sections {
		// Zero-fill sections have no file data. Names can repeat in different
		// segments (for example __TEXT/__rodata and __DATA_CONST/__rodata).
		if section.Offset == 0 {
			continue
		}
		var other *macho.Section
		for _, candidate := range mb.Sections {
			if candidate.Name == section.Name && candidate.Seg == section.Seg {
				other = candidate
				break
			}
		}
		if other == nil {
			continue
		}
		x, err := section.Data()
		if err != nil {
			continue
		}
		y, err := other.Data()
		if err != nil {
			continue
		}
		logBytes(section.Seg+"/"+section.Name, x, y)
	}
	if ma.Symtab != nil && mb.Symtab != nil {
		shown := 0
		for i, symbol := range ma.Symtab.Syms {
			if i < len(mb.Symtab.Syms) && symbol != mb.Symtab.Syms[i] {
				t.Logf("symbol %d: %+v -> %+v", i, symbol, mb.Symtab.Syms[i])
				shown++
				if shown == 10 {
					break
				}
			}
		}
	}
	da, ea := ma.DWARF()
	db, eb := mb.DWARF()
	if ea == nil && eb == nil {
		ra, rb := da.Reader(), db.Reader()
		shown := 0
		for {
			x, ex := ra.Next()
			y, ey := rb.Next()
			if ex != nil || ey != nil || x == nil || y == nil {
				break
			}
			if !reflect.DeepEqual(x, y) {
				t.Logf("DWARF: %+v -> %+v", x, y)
				shown++
				if shown == 10 {
					break
				}
			}
		}
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
