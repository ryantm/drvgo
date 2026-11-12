//go:build integration

package export

import (
	"debug/buildinfo"
	"debug/elf"
	"debug/macho"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

func TestStrippedCompilerDwarf(t *testing.T) {
	root := writeTestProject(t, map[string]string{
		"go.mod": "module example.com/stackframes\ngo 1.25\n",
		"main.go": `package main
import ("crypto/sha256"; "fmt"; "runtime"; "runtime/debug"; "strings")
func frames() {
 pcs := make([]uintptr, 16)
 n := runtime.Callers(1, pcs)
 it := runtime.CallersFrames(pcs[:n])
 for {
  f, more := it.Next()
  if strings.HasPrefix(f.Function, "main.") { fmt.Printf("%s %s:%d\n", f.Function, f.File, f.Line) }
  if !more { break }
 }
 fmt.Print(string(debug.Stack()))
}
func inline() { frames() }
func main() { inline(); fmt.Printf("%x\n", sha256.Sum256([]byte("hello"))) }
`,
		"main_test.go": "package main\nimport \"testing\"\nfunc TestFrames(t *testing.T) { main() }\n",
	})
	tc := testToolchain(t)
	cgo := false
	// Runtime PCs and instruction offsets can differ when DWARF is omitted.
	// Function names, source locations, inline frames and stack contents must
	// still agree with an ordinary Go build using the same linker flags.
	offset := regexp.MustCompile(` \+0x[0-9a-f]+`)
	for _, mode := range []struct {
		name, flags string
		omit        bool
	}{
		{"debug", "", false},
		{"strip", "-s -w -buildid=preserved/ID", true},
		{"strip-symbols", "-s", true},
		{"keep-dwarf", "-s -w=false", false},
	} {
		t.Run(mode.name, func(t *testing.T) {
			native := filepath.Join(t.TempDir(), "native")
			cmd := exec.Command(tc+"/bin/go", "build", "-trimpath", "-buildvcs=false", "-ldflags", mode.flags, "-o", native, ".")
			cmd.Dir, cmd.Env = root, append(controlledEnv(tc), "CGO_ENABLED=0", "GOPROXY=off")
			if b, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("native build: %v\n%s", err, b)
			}
			out := filepath.Join(t.TempDir(), "export")
			g, err := Generate(Options{Dir: root, Out: out, Package: ".", Toolchain: tc, CGOEnabled: &cgo, LDFlags: mode.flags})
			if err != nil {
				t.Fatal(err)
			}
			for _, u := range g.Units {
				if strings.Contains(u.Script, "/compile ") && strings.Contains(u.Script, "-dwarf=false") != mode.omit {
					t.Fatalf("unexpected DWARF setting for %s:\n%s", u.Name, u.Script)
				}
			}
			binary := buildTestExport(t, out, g)
			run := func(path string) string {
				t.Helper()
				cmd := exec.Command(path)
				// Exercise crypto self-tests as well as runtime stack metadata.
				cmd.Env = append(os.Environ(), "GODEBUG=fips140=on")
				b, err := cmd.CombinedOutput()
				if err != nil {
					t.Fatalf("run: %v\n%s", err, b)
				}
				return offset.ReplaceAllString(string(b), "")
			}
			want, got := run(native), run(binary)
			if got != want || !strings.Contains(got, "main.inline") || !strings.Contains(got, "example.com/stackframes/main.go:") {
				t.Fatalf("runtime frames differ:\n%s\nwant:\n%s", got, want)
			}
			for _, path := range []string{native, binary} {
				hasDwarf := false
				if g.GoOS == "darwin" {
					f, err := macho.Open(path)
					if err != nil {
						t.Fatal(err)
					}
					defer f.Close()
					for _, s := range f.Sections {
						hasDwarf = hasDwarf || strings.Contains(s.Name, "debug_info") && s.Size > 0
					}
				} else {
					f, err := elf.Open(path)
					if err != nil {
						t.Fatal(err)
					}
					defer f.Close()
					for _, s := range f.Sections {
						hasDwarf = hasDwarf || strings.Contains(s.Name, "debug_info") && s.Size > 0
					}
				}
				if hasDwarf == mode.omit {
					t.Fatalf("%s: DWARF present = %v", path, hasDwarf)
				}
			}
			before, err := buildinfo.ReadFile(native)
			if err != nil {
				t.Fatal(err)
			}
			after, err := buildinfo.ReadFile(binary)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(before, after) {
				t.Fatalf("build info changed:\n%v\n%v", before, after)
			}
			if mode.name == "strip" {
				id, err := exec.Command(tc+"/bin/go", "tool", "buildid", binary).Output()
				if err != nil || string(id) != "preserved/ID\n" {
					t.Fatalf("custom build ID: %q (%v)", id, err)
				}
				// The same optimization also applies to a generated test main.
				testOut := filepath.Join(t.TempDir(), "test-export")
				tg, err := Generate(Options{Dir: root, Out: testOut, Package: ".", Toolchain: tc, CGOEnabled: &cgo, LDFlags: mode.flags, Test: true})
				if err != nil {
					t.Fatal(err)
				}
				b, err := exec.Command(buildTestExport(t, testOut, tg), "-test.v").CombinedOutput()
				if err != nil || !strings.Contains(string(b), "PASS: TestFrames") {
					t.Fatalf("stripped test binary: %v\n%s", err, b)
				}
			}
		})
	}
}
