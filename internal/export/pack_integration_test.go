//go:build integration

package export

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestNativePackMatchesGo(t *testing.T) {
	tc, root := testToolchain(t), t.TempDir()
	if _, err := os.Stat(filepath.Join(tc, "bin", "libnix-pack")); !os.IsNotExist(err) {
		t.Fatalf("worker still carries a separate archive helper: %v", err)
	}
	goRun := func(args ...string) []byte {
		t.Helper()
		cmd := exec.Command(filepath.Join(tc, "bin", "go"), args...)
		cmd.Env = append(controlledEnv(tc), "GOPROXY=off")
		b, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("go %v: %v\n%s", args, err, b)
		}
		return b
	}
	// Resolve the SDK's own reference implementation, including Go releases
	// that build auxiliary tools on demand.
	pack := strings.TrimSpace(string(goRun("tool", "-n", "pack")))
	files := []string{"odd space.o", "even.o", "unicode-雪.o", "long-native-object-name.o"}
	for i, name := range files {
		if err := os.WriteFile(filepath.Join(root, name), bytes.Repeat([]byte{0, 255, byte(i)}, i+1), 0755); err != nil {
			t.Fatal(err)
		}
	}
	src, obj := filepath.Join(root, "source.go"), filepath.Join(root, "compiled.a")
	if err := os.WriteFile(src, []byte("package fixture\nconst Value = 42\nfunc Answer() int { return Value }\n"), 0644); err != nil {
		t.Fatal(err)
	}
	goRun("tool", "compile", "-pack", "-p", "fixture", "-o", obj, src)
	for _, first := range []string{"c", "grc", "r", "cv"} {
		t.Run(first, func(t *testing.T) {
			dir := filepath.Join(root, first)
			if err := os.Mkdir(dir, 0755); err != nil {
				t.Fatal(err)
			}
			reference := filepath.Join(dir, "reference.a")
			var script strings.Builder
			var referenceOutput []byte
			for i := 0; i < 96; i++ {
				op := "r"
				if i == 0 {
					op = first
				}
				inputs := []string{filepath.Join(root, files[i%len(files)])}
				if i < 2 {
					inputs = append([]string{obj}, inputs...)
				}
				cmd := exec.Command(pack, append([]string{op, reference}, inputs...)...)
				b, err := cmd.CombinedOutput()
				if err != nil {
					t.Fatalf("reference pack: %v\n%s", err, b)
				}
				referenceOutput = append(referenceOutput, b...)
				fmt.Fprintf(&script, "libnix-pack %s @SELF@/_pkg_.a", op)
				for _, input := range inputs {
					fmt.Fprintf(&script, " %s", replayQuote(input))
				}
				script.WriteByte('\n')
			}
			// A second archive after the long sequence exercises state reset.
			fmt.Fprintf(&script, "libnix-pack cv @SELF@/other.a %s\n", replayQuote(obj))
			b, err := exec.Command(pack, "cv", filepath.Join(dir, "other.a"), obj).CombinedOutput()
			if err != nil {
				t.Fatalf("reference pack: %v\n%s", err, b)
			}
			referenceOutput = append(referenceOutput, b...)
			path := filepath.Join(dir, "script")
			if err := os.WriteFile(path, []byte(script.String()), 0644); err != nil {
				t.Fatal(err)
			}
			out := filepath.Join(dir, "out")
			// Leaking even one archive descriptor per action exceeds this limit.
			// The test host's shell sets the child limit; the worker itself
			// does not need a shell in its toolchain.
			cmd := exec.Command("sh", "-c", `ulimit -n 64 && exec "$1"`, "pack-test", filepath.Join(tc, "bin", "drvgo-builder"))
			cmd.Env = append(os.Environ(), "TMPDIR="+dir, "scriptPath="+path, "out="+out, "isBinary=0", "emptyTests=0", "NIX_BUILD_CORES=1")
			b, err = cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("native pack: %v\n%s", err, b)
			}
			if !bytes.Equal(b, referenceOutput) {
				t.Fatalf("native pack output differs from Go:\n%s\nwant:\n%s", b, referenceOutput)
			}
			want, err := os.ReadFile(reference)
			if err != nil {
				t.Fatal(err)
			}
			got, err := os.ReadFile(filepath.Join(out, "_pkg_.a"))
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, want) {
				t.Fatalf("archive differs: native %d bytes, Go %d bytes", len(got), len(want))
			}
		})
	}
}
