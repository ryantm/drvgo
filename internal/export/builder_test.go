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

func TestBuilderPublishesOnlyExecutable(t *testing.T) {
	root := t.TempDir()
	script := filepath.Join(root, "script")
	// The command archive can contain the same large embedded assets as the
	// executable. Nothing imports a main package, so keep only its executable.
	if err := os.WriteFile(script, []byte(`
mkdir -p @SELF@/exe
echo archive > @SELF@/_pkg_.a
echo executable > @SELF@/exe/a.out
chmod +x @SELF@/exe/a.out
`), 0644); err != nil {
		t.Fatal(err)
	}
	builder, _ := templates.ReadFile("templates/builder.sh")
	out := filepath.Join(root, "out")
	c := exec.Command("bash", "-c", string(builder))
	c.Env = append(os.Environ(), "TMPDIR="+root, "out="+out, "scriptPath="+script, "isBinary=1", "binaryName=command", "NIX_BUILD_CORES=1")
	if log, err := c.CombinedOutput(); err != nil {
		t.Fatalf("builder: %v %s", err, log)
	}
	if _, err := os.Stat(filepath.Join(out, "_pkg_.a")); !os.IsNotExist(err) {
		t.Fatalf("command archive was published: %v", err)
	}
	info, err := os.Stat(filepath.Join(out, "bin", "command"))
	if err != nil || info.Mode()&0111 == 0 {
		t.Fatalf("executable missing or not executable: %v", err)
	}
}
