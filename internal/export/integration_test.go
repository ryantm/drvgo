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
		if _, err := Generate(Options{Dir: root, Out: out, Package: ".", Toolchain: os.Getenv("DRVGO_TEST_TOOLCHAIN")}); err != nil {
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
		c := exec.Command("nix-build", out, "-A", "default", "--no-out-link", "--max-jobs", "4", "--cores", "2")
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
	// A moved export must produce the same recipes using the pinned fallback.
	resolvedFile := filepath.Join(out, "resolved-toolchain.nix")
	resolved, err := os.ReadFile(resolvedFile)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(resolved), "builtins.appendContext") {
		t.Fatal("resolved toolchain fast path was not generated")
	}
	fallback := strings.Replace(string(resolved), "if builtins.pathExists path && builtins.pathExists drv then", "if false then", 1)
	if err = os.WriteFile(resolvedFile, []byte(fallback), 0644); err != nil {
		t.Fatal(err)
	}
	defaultTC, err := Toolchain()
	if err != nil {
		t.Fatal(err)
	}
	selectedTC := os.Getenv("DRVGO_TEST_TOOLCHAIN")
	if selectedTC == "" || selectedTC == defaultTC {
		for id, d := range derivations() {
			if before[id] != d {
				t.Fatalf("toolchain fallback changed %s", id)
			}
		}
	} else {
		b, err := exec.Command("nix-instantiate", out, "-A", "default").CombinedOutput()
		if err == nil || !strings.Contains(string(b), "Refusing to substitute") {
			t.Fatalf("custom toolchain fallback: %v\n%s", err, b)
		}
	}
	if err = os.WriteFile(resolvedFile, resolved, 0644); err != nil {
		t.Fatal(err)
	}

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
	// Scheduling settings and files outside the selected production graph must
	// not perturb recipes. Re-discovering inputs must still notice new embeds.
	for _, cores := range []string{"1", "7"} {
		t.Setenv("GOMAXPROCS", cores)
		generate()
		for id, d := range derivations() {
			if before[id] != d {
				t.Fatalf("planner GOMAXPROCS=%s changed %s", cores, id)
			}
		}
	}
	for name, content := range map[string]string{
		"dep/dep_test.go":     "package dep\n// test-only change\n",
		"unrelated/unused.go": "package unrelated\nconst Value = 1\n",
	} {
		path := filepath.Join(root, name)
		if err = os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	generate()
	for id, d := range derivations() {
		if before[id] != d {
			t.Fatalf("test/unrelated files changed %s", id)
		}
	}
	// Content hashes must change even when a source edit preserves file size.
	depPath := filepath.Join(root, "dep/dep.go")
	original, err := os.ReadFile(depPath)
	if err != nil {
		t.Fatal(err)
	}
	edited := strings.ReplaceAll(string(original), "stable", "edited")
	if err = os.WriteFile(depPath, []byte(edited), 0644); err != nil {
		t.Fatal(err)
	}
	generate()
	changedByGo := 0
	for id, d := range derivations() {
		if before[id] != d {
			changedByGo++
		}
	}
	if changedByGo != 2 {
		t.Fatalf("Go edit changed %d packages; want 2", changedByGo)
	}
	if got := output(build()); !strings.Contains(got, "edited\n") {
		t.Fatalf("Go edit missing: %q", got)
	}
	if err = os.WriteFile(depPath, original, 0644); err != nil {
		t.Fatal(err)
	}
	newEmbed := filepath.Join(root, "message/assets/new.txt")
	if err = os.WriteFile(newEmbed, []byte("new glob match\n"), 0644); err != nil {
		t.Fatal(err)
	}
	generate()
	changedByAddition := 0
	for id, d := range derivations() {
		if before[id] != d {
			changedByAddition++
		}
	}
	if changedByAddition != 2 {
		t.Fatalf("new embed changed %d packages; want 2", changedByAddition)
	}
	if err = os.Remove(newEmbed); err != nil {
		t.Fatal(err)
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
