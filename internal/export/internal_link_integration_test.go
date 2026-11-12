//go:build integration

package export

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestInternalLinkToolchain(t *testing.T) {
	tc := testToolchain(t)
	data, err := os.ReadFile(filepath.Join(tc, "nix-support", "drvgo-internal-link-toolchain"))
	if err != nil {
		t.Fatal(err)
	}
	compact := strings.TrimSpace(string(data))
	compiler, archArgs := "gcc", []string{}
	if runtime.GOARCH == "amd64" {
		archArgs = []string{"-m64"}
	}
	if runtime.GOOS == "darwin" {
		compiler, archArgs = "clang", []string{"-arch", "arm64"}
	}
	for _, query := range []string{"--print-libgcc-file-name", "--print-file-name=libc_nonshared.a", "--print-file-name=libssp_nonshared.a"} {
		args := append(append([]string{}, archArgs...), query)
		before, err := exec.Command(tc+"/bin/"+compiler, args...).Output()
		if err != nil {
			t.Fatal(err)
		}
		after, err := exec.Command(compact+"/bin/"+compiler, args...).Output()
		if err != nil {
			t.Fatal(err)
		}
		original, err := os.ReadFile(strings.TrimSpace(string(before)))
		if err != nil {
			if !bytes.Equal(before, after) {
				t.Fatalf("changed missing-library query %s: %q -> %q", query, before, after)
			}
			continue
		}
		copied, err := os.ReadFile(strings.TrimSpace(string(after)))
		if err != nil || !bytes.Equal(original, copied) {
			t.Fatalf("changed runtime archive %s: %v", query, err)
		}
	}
	for _, args := range [][]string{nil, {"-c", "source.c"}, append(append([]string{}, archArgs...), "--print-file-name=unexpected.a")} {
		if output, err := exec.Command(compact+"/bin/"+compiler, args...).Output(); err == nil || len(output) != 0 {
			t.Fatalf("accepted unsupported query %v: %q, %v", args, output, err)
		}
	}
	for _, mode := range []struct {
		name                             string
		cgo, race, forceCgo, wantRuntime bool
	}{
		{"pure", false, false, false, false},
		// Darwin's net and os/user call libc through Go syscall wrappers.
		// Enabling cgo alone does not add native objects to this program.
		{"cgo-enabled", true, false, false, runtime.GOOS != "darwin"},
		{"native", true, false, true, true},
		{"race", true, true, false, true},
	} {
		t.Run(mode.name, func(t *testing.T) {
			files := map[string]string{
				"go.mod": "module example.com/internallink\ngo 1.25\n",
				"main.go": `package main
import ("fmt"; "net"; "os/user")
func main() {
 ips, err := net.LookupIP("localhost"); if err != nil || len(ips) == 0 { panic("localhost lookup failed") }
 u, err := user.Current(); if err != nil || u.Uid == "" { panic("user lookup failed") }
 fmt.Println("native lookups work")
}
`,
			}
			if mode.forceCgo {
				files["native.go"] = "package main\nimport _ \"runtime/cgo\"\n"
			}
			source := writeTestProject(t, files)
			out := filepath.Join(t.TempDir(), "export")
			g, err := Generate(Options{Dir: source, Out: out, Package: ".", Toolchain: tc, CGOEnabled: &mode.cgo, Race: mode.race, LDFlags: "-linkmode=internal -buildid=internal/test"})
			if err != nil {
				t.Fatal(err)
			}
			if g.NativeLink || g.InternalLink != mode.wantRuntime {
				t.Fatalf("unexpected link selection: native=%v internal=%v", g.NativeLink, g.InternalLink)
			}
			binary := buildTestExport(t, out, g)
			cmd := exec.Command(binary)
			cmd.Env = append(os.Environ(), "GODEBUG=netdns=cgo")
			if b, err := cmd.CombinedOutput(); err != nil || string(b) != "native lookups work\n" {
				t.Fatalf("run: %s (%v)", b, err)
			}
			// Compare every case with the original full native bundle. Removing
			// the optional role also exercises older/custom toolchain fallback
			// when native objects are present.
			expression := "let tc = import " + out + "/resolved-toolchain.nix; in " +
				"(builtins.removeAttrs tc [ \"internalLink\" ]) // { link = tc.native; }"
			cmd = exec.Command("nix-build", out, "-A", "default", "--no-out-link", "--arg", "toolchain", expression, "--max-jobs", "4", "--cores", "2")
			var stderr strings.Builder
			cmd.Stderr = &stderr
			b, err := cmd.Output()
			if err != nil {
				t.Fatalf("fallback: %v\n%s", err, stderr.String())
			}
			original, err := os.ReadFile(filepath.Join(strings.TrimSpace(string(b)), "bin", g.BinaryName))
			if err != nil {
				t.Fatal(err)
			}
			actual, err := os.ReadFile(binary)
			if err != nil || !bytes.Equal(original, actual) {
				t.Fatalf("internal-link bundle changed executable: %v", err)
			}
		})
	}
}
