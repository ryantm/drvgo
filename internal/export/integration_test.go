//go:build integration

package export

import (
	"encoding/json"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestEmbeddedAssetsAndCacheIsolation(t *testing.T) {
	root := t.TempDir()
	fixture, err := filepath.Abs("../../tests/fixture")
	if err != nil {
		t.Fatal(err)
	}
	err = filepath.WalkDir(fixture, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(fixture, path)
		if d.IsDir() {
			if d.Name() == ".drvgo" {
				return filepath.SkipDir
			}
			return os.MkdirAll(filepath.Join(root, rel), 0755)
		}
		if d.Type()&os.ModeSymlink != 0 {
			return nil
		}
		return copyFile(path, filepath.Join(root, rel))
	})
	if err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(root, "export")
	generate := func() {
		t.Helper()
		if _, err := Generate(Options{Dir: root, Out: out, Package: "."}); err != nil {
			t.Fatal(err)
		}
	}
	derivations := func() map[string]string {
		t.Helper()
		c := exec.Command("nix-instantiate", "--eval", "--strict", "--json", "--expr", `builtins.mapAttrs (_: d: d.drvPath) (import `+out+` {}).units`)
		b, err := c.CombinedOutput()
		if err != nil {
			t.Fatalf("instantiate: %v\n%s", err, b)
		}
		var m map[string]string
		if err = json.Unmarshal(b, &m); err != nil {
			t.Fatal(err)
		}
		return m
	}
	build := func() string {
		t.Helper()
		c := exec.Command("nix-build", out, "-A", "default", "--no-out-link", "--max-jobs", "4")
		var stderr strings.Builder
		c.Stderr = &stderr
		b, err := c.Output()
		if err != nil {
			t.Fatalf("build: %v\n%s", err, stderr.String())
		}
		return strings.TrimSpace(string(b))
	}
	output := func(path string) string {
		t.Helper()
		b, err := exec.Command(path + "/bin/libnix-fixture").CombinedOutput()
		if err != nil {
			t.Fatalf("run: %v\n%s", err, b)
		}
		return string(b)
	}
	generate()
	before := derivations()
	first := build()
	if got := output(first); got != "hello embedded world\nstable\nnested asset\n01 02 ff\nhidden asset\n" {
		t.Fatalf("unexpected embedded output: %q", got)
	}
	generate()
	again := derivations()
	for id, d := range before {
		if again[id] != d {
			t.Fatalf("unchanged export changed %s", id)
		}
	}
	if got := build(); got != first {
		t.Fatal("cache miss on unchanged build")
	}
	if err = os.WriteFile(filepath.Join(root, "message/assets/message.txt"), []byte("changed embed\n"), 0644); err != nil {
		t.Fatal(err)
	}
	generate()
	after := derivations()
	changed := 0
	for id, d := range before {
		if after[id] != d {
			changed++
		}
	}
	if changed != 2 {
		t.Fatalf("embed change invalidated %d units; want embedding package and command only", changed)
	}
	// Remove original sources before realizing the changed export.
	for _, name := range []string{"message", "dep", "cgobase", "cgouse", "main.go", "native_shared.h", "go.mod"} {
		if err = os.RemoveAll(filepath.Join(root, name)); err != nil {
			t.Fatal(err)
		}
	}
	second := build()
	if !strings.HasPrefix(output(second), "changed embed\nstable\n") {
		t.Fatal("changed asset missing from standalone binary")
	}
	if got := build(); got != second {
		t.Fatal("standalone export changed after removing source checkout")
	}
	t.Logf("%d package derivations; unchanged build reused output; embed edit changed exactly %d", len(before), changed)
}
