package export

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProjectRequirements(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "module", "subdir")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	write := func(path, s string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(s), 0644); err != nil {
			t.Fatal(err)
		}
	}
	mod := filepath.Join(root, "module", "go.mod")
	work := filepath.Join(root, "go.work")
	write(mod, "module example\ngo 1.26.6 // minimum\n")
	if _, err := checkRequirements(dir, "go1.26.5"); err == nil || !strings.Contains(err.Error(), "go.mod requires") {
		t.Fatalf("minimum: %v", err)
	}
	write(mod, "module example\ngo 1.25.0\ntoolchain go1.26.6\n")
	ds, err := checkRequirements(dir, "go1.26.5")
	if err != nil || len(ds) != 1 {
		t.Fatalf("suggestion: %v %v", ds, err)
	}
	write(work, "go 1.25.0\ntoolchain default\nuse ./module\n")
	ds, err = checkRequirements(dir, "go1.26.5")
	if err != nil || len(ds) != 0 {
		t.Fatalf("workspace should override module suggestion: %v %v", ds, err)
	}
	write(work, "go 1.27.0\nuse ./module\n")
	if _, err := checkRequirements(dir, "go1.26.6"); err == nil || !strings.Contains(err.Error(), "go.work requires") {
		t.Fatalf("workspace minimum: %v", err)
	}
}
