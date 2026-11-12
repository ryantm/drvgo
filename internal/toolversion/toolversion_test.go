package toolversion

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestPinnedVersionAndRejectedExecution(t *testing.T) {
	path := filepath.Join(t.TempDir(), "compile")
	want := "compile version go1.26.5 X:custom-experiment\n"
	if err := os.WriteFile(path+".version", []byte(want), 0644); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := Print(&out, path, []string{"-V=full"}); err != nil || out.String() != want {
		t.Fatalf("version = %q, error = %v", out.String(), err)
	}
	for _, args := range [][]string{nil, {"-V"}, {"-o", "output", "input.go"}, {"-V=full", "input.go"}} {
		out.Reset()
		if err := Print(&out, path, args); err == nil || out.Len() != 0 {
			t.Fatalf("accepted non-version invocation %q", args)
		}
	}
	if err := Print(&out, path+"-missing", []string{"-V=full"}); err == nil {
		t.Fatal("accepted unknown tool")
	}
	if err := Print(&out, filepath.Join(t.TempDir(), "asm"), []string{"-V=full"}); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing version metadata: %v", err)
	}
}
