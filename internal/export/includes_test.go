package export

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStandardIncludeClosure(t *testing.T) {
	root, dest := t.TempDir(), t.TempDir()
	files := map[string]string{
		"runtime/asm.s":            "#include \"generated.h\"\n#include \"cgo/abi.h\"\n",
		"runtime/cgo/abi.h":        "#include \"../../internal/shared.h\"\n",
		"internal/shared.h":        "#include \"../runtime/cgo/abi.h\"\n",
		"runtime/cgo/unrelated.go": "package unrelated",
	}
	for name, contents := range files {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(contents), 0644); err != nil {
			t.Fatal(err)
		}
	}
	if err := collectIncludes(root, []string{"runtime/asm.s"}, func(src, rel string) error {
		return copyFile(src, filepath.Join(dest, rel))
	}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"runtime/cgo/abi.h", "internal/shared.h"} {
		got, err := os.ReadFile(filepath.Join(dest, name))
		if err != nil || string(got) != files[name] {
			t.Fatalf("%s: %q %v", name, got, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dest, "runtime/cgo/unrelated.go")); !os.IsNotExist(err) {
		t.Fatal("copied unrelated package source")
	}
}
