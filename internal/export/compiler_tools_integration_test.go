//go:build integration

package export

import (
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The compact GCC bundle must preserve its actual LTO backend. lto-dump is a
// separate inspector and is not needed to compile or link these programs.
func TestNativeCompilerLTO(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("GCC bundle compaction applies only on Linux")
	}
	tc := testToolchain(t)
	cmd := exec.Command(tc+"/bin/gcc", "--version")
	cmd.Env = controlledEnv(tc)
	version, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("compiler version: %s (%v)", version, err)
	}
	if !strings.Contains(string(version), "GCC") {
		t.Skip("GCC bundle compaction does not apply to this compiler")
	}
	root := writeTestProject(t, map[string]string{
		"probe.c": `#include <stdio.h>
#include <stdint.h>
int main(void) { uint64_t x = 42; printf("%llu\n", (unsigned long long)x); return 0; }
`,
		"probe.cc": `#include <vector>
#include <iostream>
int main() {
 std::vector<int> values{20,22};
 try { throw values[0]+values[1]; }
 catch (int answer) { std::cout << answer << "\n"; }
}
`,
	})
	for _, language := range []struct{ file, compiler string }{{"probe.c", "gcc"}, {"probe.cc", "g++"}} {
		for _, mode := range []string{"plain", "lto"} {
			t.Run(language.compiler+"/"+mode, func(t *testing.T) {
				binary := filepath.Join(t.TempDir(), "probe")
				args := []string{"-O2"}
				if mode == "lto" {
					args = append(args, "-flto")
				}
				args = append(args, filepath.Join(root, language.file), "-o", binary)
				cmd := exec.Command(tc+"/bin/"+language.compiler, args...)
				cmd.Env = controlledEnv(tc)
				if output, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("compile: %s (%v)", output, err)
				}
				if output, err := exec.Command(binary).CombinedOutput(); err != nil || string(output) != "42\n" {
					t.Fatalf("run: %s (%v)", output, err)
				}
			})
		}
	}
}
