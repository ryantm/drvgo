package export

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/ryantm/drvgo/internal/plan"
)

func TestInlineSourceSelectionAndInvalidation(t *testing.T) {
	dir := t.TempDir()
	write := func(id, name string, data []byte) {
		t.Helper()
		path := filepath.Join(dir, "sources", id, name)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0644); err != nil {
			t.Fatal(err)
		}
	}
	write("small", "asset", []byte{0, 255, '\n', '@', '$'})
	write("large", "a", []byte("small first file"))
	write("large", "z", []byte(strings.Repeat("x", inlineSourceLimit)))
	write("unrelated", "file", []byte("initial"))
	generate := func() plan.Unit {
		t.Helper()
		units := []plan.Unit{{Sources: []string{"large", "small"}, Script: "tool @SRC:small@/asset @SRC:large@/z\n"}}
		if err := inlineSources(dir, units); err != nil {
			t.Fatal(err)
		}
		return units[0]
	}
	before := generate()
	if !reflect.DeepEqual(before.Sources, []string{"large"}) || strings.Contains(before.Script, "@SRC:small@") || !strings.Contains(before.Script, "@SRC:large@/z") {
		t.Fatalf("incorrect source selection: %+v", before)
	}
	if !strings.Contains(before.Script, base64.StdEncoding.EncodeToString([]byte{0, 255, '\n', '@', '$'})) {
		t.Fatal("binary source bytes changed")
	}
	write("unrelated", "file", []byte("changed"))
	if !reflect.DeepEqual(before, generate()) {
		t.Fatal("unrelated source changed package recipe")
	}
	write("small", "asset", []byte("changed"))
	if reflect.DeepEqual(before, generate()) {
		t.Fatal("changed source reused package recipe")
	}
}

func TestInlineSnapshotLimit(t *testing.T) {
	for _, size := range []int{inlineSourceLimit - 65, inlineSourceLimit - 64, inlineSourceLimit * 2} {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "a"), []byte(strings.Repeat("x", size)), 0644); err != nil {
			t.Fatal(err)
		}
		script, err := inlineSnapshot(dir, "dest")
		if err != nil {
			t.Fatal(err)
		}
		if (script != "") != (size+65 <= inlineSourceLimit) {
			t.Fatalf("incorrect cutoff for %d bytes", size)
		}
	}
	if _, err := inlineSnapshot(filepath.Join(t.TempDir(), "missing"), "dest"); err == nil {
		t.Fatal("missing snapshot accepted")
	}
}
