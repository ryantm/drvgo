//go:build integration

package export

import (
	"bytes"
	"debug/elf"
	"debug/macho"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestNativeCompilerDwarf(t *testing.T) {
	root := writeTestProject(t, map[string]string{
		"go.mod":  "module example.com/nativedwarf\ngo 1.25\n",
		"main.go": "package main\nimport (\"fmt\"; \"example.com/nativedwarf/native\")\nfunc main() { fmt.Println(native.Value()) }\n",
		"native/native.go": `package native
/*
typedef struct { long left; double right; } pair;
enum { ANSWER = 42 };
static long value(pair p) { return p.left + (long)p.right; }
*/
import "C"
func Value() int {
 p := C.pair{left: 20, right: 22}
 return int(C.value(p)) + int(C.ANSWER) - 42
}
`,
	})
	tc := testToolchain(t)
	for _, mode := range []struct {
		name, flags string
		debug       bool
	}{
		{"debug", "", true},
		{"stripped", "-s -w -buildid=native/kept", false},
		{"keep-debug", "-s -w=false", true},
	} {
		t.Run(mode.name, func(t *testing.T) {
			out := filepath.Join(t.TempDir(), "export")
			g, err := Generate(Options{Dir: root, Out: out, Package: ".", Toolchain: tc, LDFlags: mode.flags})
			if err != nil {
				t.Fatal(err)
			}
			binary := buildTestExport(t, out, g)
			if b, err := exec.Command(binary).CombinedOutput(); err != nil || string(b) != "42\n" {
				t.Fatalf("cgo types or execution failed: %v\n%s", err, b)
			}
			id := ""
			for _, u := range g.Units {
				if u.Name == "example.com/nativedwarf/native" {
					id = u.ID
					if strings.Contains(u.Script, " -g0 -c ") == mode.debug {
						t.Fatalf("unexpected native compiler setting:\n%s", u.Script)
					}
				}
			}
			if id == "" {
				t.Fatal("missing native dependency")
			}
			path, err := exec.Command("nix-instantiate", "--eval", "--raw", "--expr", "(import "+out+" {}).units."+id+".outPath").Output()
			if err != nil {
				t.Fatal(err)
			}
			if got := nativeArchiveHasDebug(t, filepath.Join(strings.TrimSpace(string(path)), "_pkg_.a"), g.GoOS); got != mode.debug {
				t.Fatalf("native object DWARF present = %v; want %v", got, mode.debug)
			}
			if mode.name == "stripped" {
				b, err := exec.Command(tc+"/bin/go", "tool", "buildid", binary).Output()
				if err != nil || string(b) != "native/kept\n" {
					t.Fatalf("custom native build ID: %q (%v)", b, err)
				}
			}
		})
	}
}

func nativeArchiveHasDebug(t *testing.T, path, goos string) bool {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil || !bytes.HasPrefix(data, []byte("!<arch>\n")) {
		t.Fatalf("read native archive: %v", err)
	}
	objects, debug := 0, false
	for pos := 8; pos < len(data); {
		if pos+60 > len(data) {
			t.Fatal("truncated archive header")
		}
		header := data[pos : pos+60]
		size, err := strconv.Atoi(strings.TrimSpace(string(header[48:58])))
		if err != nil || size < 0 || pos+60+size > len(data) || string(header[58:60]) != "`\n" {
			t.Fatal("invalid archive member")
		}
		name := strings.TrimSuffix(strings.TrimSpace(string(header[:16])), "/")
		member := data[pos+60 : pos+60+size]
		pos += 60 + size + size%2
		// cmd/go gives its generated native objects these short names on
		// every supported platform; Go object members are not ELF/Mach-O.
		if !strings.HasPrefix(name, "_x") || !strings.HasSuffix(name, ".o") {
			continue
		}
		objects++
		if goos == "darwin" {
			f, err := macho.NewFile(bytes.NewReader(member))
			if err != nil {
				t.Fatal(err)
			}
			for _, section := range f.Sections {
				debug = debug || strings.Contains(section.Name, "debug_info") && section.Size > 0
			}
			f.Close()
		} else {
			f, err := elf.NewFile(bytes.NewReader(member))
			if err != nil {
				t.Fatal(err)
			}
			for _, section := range f.Sections {
				debug = debug || strings.Contains(section.Name, "debug_info") && section.Size > 0
			}
			f.Close()
		}
	}
	if objects == 0 {
		t.Fatal("archive contains no native objects")
	}
	return debug
}
