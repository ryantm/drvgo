package modcache

import (
	"archive/zip"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ryantm/drvgo/internal/testutil"
)

func testProxy(t *testing.T, bad bool) string {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "example.com", "!upper", "dep", "@v")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	mod := "module example.com/Upper/dep\ngo 1.23\n"
	for name, content := range map[string]string{
		"v1.0.0.mod":  mod,
		"v1.0.0.info": `{"Version":"v1.0.0","Time":"2020-01-01T00:00:00Z"}`,
		"list":        "v1.0.0\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	f, err := os.Create(filepath.Join(dir, "v1.0.0.zip"))
	if err != nil {
		t.Fatal(err)
	}
	z := zip.NewWriter(f)
	files := map[string]string{"go.mod": mod, "dep.go": "package dep\nconst Value = 42\n"}
	if bad {
		files["../escape.go"] = "package escape\n"
	}
	for name, content := range files {
		w, err := z.Create("example.com/Upper/dep@v1.0.0/" + name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	// A graph can load metadata for modules whose zip is never downloaded.
	metadata := filepath.Join(root, "example.com", "metadata", "@v")
	if err := os.MkdirAll(metadata, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(metadata, "v1.0.0.mod"), []byte("module example.com/metadata\n"), 0644); err != nil {
		t.Fatal(err)
	}
	return root
}

func cacheOutput(t *testing.T) string {
	t.Helper()
	out := filepath.Join(t.TempDir(), "cache")
	t.Cleanup(func() {
		// cmd/go makes extracted module directories read-only.
		filepath.WalkDir(out, func(path string, entry fs.DirEntry, err error) error {
			if err == nil && entry.IsDir() {
				return os.Chmod(path, 0755)
			}
			return nil
		})
	})
	return out
}

func TestPrepareReadOnlyCache(t *testing.T) {
	goTool, err := exec.LookPath("go")
	if err != nil {
		t.Fatal(err)
	}
	out := cacheOutput(t)
	// The builder must ignore ambient private-module and proxy settings.
	t.Setenv("GONOPROXY", "*")
	t.Setenv("GOPROXY", "file:///must-not-be-used")
	if err := Prepare(goTool, testProxy(t, false), out); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{
		"example.com/!upper/dep@v1.0.0/dep.go",
		"cache/download/example.com/!upper/dep/@v/v1.0.0.ziphash",
		"cache/download/example.com/metadata/@v/v1.0.0.mod",
	} {
		if _, err := os.Stat(filepath.Join(out, name)); err != nil {
			t.Fatal(err)
		}
	}
	if err := filepath.WalkDir(out, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if strings.HasSuffix(path, ".zip") || strings.HasSuffix(path, ".lock") {
			t.Errorf("redundant cache file %s", path)
		}
		mode := os.FileMode(0444)
		if entry.IsDir() {
			mode = 0555
		}
		return os.Chmod(path, mode)
	}); err != nil {
		t.Fatal(err)
	}
	project := t.TempDir()
	if err := os.WriteFile(filepath.Join(project, "go.mod"), []byte("module example.com/app\ngo 1.23\nrequire example.com/Upper/dep v1.0.0\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, "main.go"), []byte("package main\nimport d \"example.com/Upper/dep\"\nfunc main(){println(d.Value)}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(goTool, "build", "-mod=mod", "-o", filepath.Join(project, "app"), ".")
	cmd.Dir = project
	cmd.Env = append(os.Environ(), "GOMODCACHE="+out, "GOPROXY=off", "GONOPROXY=", "GOSUMDB=off", "GOENV=off", "GOFLAGS="+testutil.GoFlags(), "GOWORK=off", "GOTOOLCHAIN=local", "CGO_ENABLED=0")
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("read-only offline build: %v\n%s", err, b)
	}
	if b, err := exec.Command(filepath.Join(project, "app")).CombinedOutput(); err != nil || string(b) != "42\n" {
		t.Fatalf("prepared module result: %v: %s", err, b)
	}
	// Retaining .ziphash must not bypass the consuming project's go.sum.
	if err := os.WriteFile(filepath.Join(project, "go.sum"), []byte("example.com/Upper/dep v1.0.0 h1:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=\n"), 0644); err != nil {
		t.Fatal(err)
	}
	invalid := exec.Command(goTool, cmd.Args[1:]...)
	invalid.Dir, invalid.Env = cmd.Dir, cmd.Env
	if b, err := invalid.CombinedOutput(); err == nil || !strings.Contains(string(b), "checksum mismatch") {
		t.Fatalf("accepted mismatched go.sum: %v: %s", err, b)
	}
}

func TestPrepareRejectsInvalidZip(t *testing.T) {
	goTool, err := exec.LookPath("go")
	if err != nil {
		t.Fatal(err)
	}
	if err := Prepare(goTool, testProxy(t, true), cacheOutput(t)); err == nil {
		t.Fatal("accepted invalid module zip")
	}
}

func TestPrepareDoesNotReplaceExistingOutput(t *testing.T) {
	out := t.TempDir()
	file := filepath.Join(out, "existing")
	if err := os.WriteFile(file, []byte("keep"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := Prepare("must-not-run", "must-not-read", out); err == nil {
		t.Fatal("accepted existing output")
	}
	if b, err := os.ReadFile(file); err != nil || string(b) != "keep" {
		t.Fatalf("changed existing output: %v: %s", err, b)
	}
}
