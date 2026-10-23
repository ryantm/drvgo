package export

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestBuilderCoreBudget(t *testing.T) {
	for _, cores := range []string{"1", "2", "0", "invalid"} {
		t.Run(cores, func(t *testing.T) {
			root := t.TempDir()
			script := filepath.Join(root, "script")
			if err := os.WriteFile(script, []byte(`printf '%s %s' '@CORES@' "$GOMAXPROCS" > @SELF@/_pkg_.a`), 0644); err != nil {
				t.Fatal(err)
			}
			builder, err := templates.ReadFile("templates/builder.sh")
			if err != nil {
				t.Fatal(err)
			}
			out := filepath.Join(root, "out")
			c := exec.Command("bash", "-c", string(builder))
			c.Env = append(os.Environ(), "TMPDIR="+root, "out="+out, "scriptPath="+script, "isBinary=0", "NIX_BUILD_CORES="+cores)
			log, err := c.CombinedOutput()
			if cores == "invalid" {
				if err == nil || !strings.Contains(string(log), "invalid NIX_BUILD_CORES") {
					t.Fatalf("invalid budget accepted: %v %s", err, log)
				}
				return
			}
			if err != nil {
				t.Fatalf("builder: %v %s", err, log)
			}
			b, err := os.ReadFile(filepath.Join(out, "_pkg_.a"))
			if err != nil {
				t.Fatal(err)
			}
			values := strings.Fields(string(b))
			if len(values) != 2 || values[0] != values[1] {
				t.Fatalf("compiler and runtime budgets differ: %q", b)
			}
			n, err := strconv.Atoi(values[0])
			if err != nil || n < 1 || (cores != "0" && values[0] != cores) {
				t.Fatalf("unexpected budget for %s: %q", cores, b)
			}
		})
	}
}
