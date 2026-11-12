package export

import (
	"bytes"
	"compress/zlib"
	"encoding/base64"
	"io"
	"math/rand/v2"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/ryantm/drvgo/internal/plan"
)

func inlineSnapshot(root, dest string) (string, error) {
	var encoder sourceEncoder
	return encoder.snapshot(root, dest, nil)
}

func TestCompressedSnapshotBudgetAndReuse(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "asset")
	data := bytes.Repeat([]byte{0, 255, '\n', '@', '$'}, 6000)
	write := func(data []byte) {
		t.Helper()
		if err := os.WriteFile(path, data, 0755); err != nil {
			t.Fatal(err)
		}
	}
	write(data)
	var encoder sourceEncoder
	first, err := encoder.snapshot(dir, "dest", nil)
	if err != nil {
		t.Fatal(err)
	}
	line := strings.Split(strings.TrimSpace(first), "\n")[1]
	fields := strings.Fields(line)
	if fields[0] != "drvgo-source-zlib" || fields[1] != "0755" || fields[3] != "30000" {
		t.Fatalf("missing compressed source or mode/size: %s", line)
	}
	encoded, err := base64.StdEncoding.DecodeString(strings.Trim(fields[4], "'"))
	if err != nil {
		t.Fatal(err)
	}
	r, err := zlib.NewReader(bytes.NewReader(encoded))
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := io.ReadAll(r)
	r.Close()
	if err != nil || !bytes.Equal(decoded, data) {
		t.Fatalf("compressed bytes changed: %v", err)
	}
	noise := make([]byte, 30000)
	rand.NewChaCha8([32]byte{1}).Read(noise)
	write(noise)
	if script, err := encoder.snapshot(dir, "dest", nil); err != nil || script != "" {
		t.Fatalf("oversized incompressible payload accepted: %v", err)
	}
	write(data)
	if script, err := encoder.snapshot(dir, "dest", nil); err != nil || script != first {
		t.Fatalf("compressor reuse changed the restored recipe: %v", err)
	}
	// The payload budget is shared by all files, not applied to each file.
	write(noise[:9000])
	if err := os.WriteFile(filepath.Join(dir, "second"), noise[9000:18000], 0644); err != nil {
		t.Fatal(err)
	}
	if script, err := encoder.snapshot(dir, "dest", nil); err != nil || script != "" {
		t.Fatalf("oversized aggregate payload accepted: %v", err)
	}
}

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
		if err := prepareSources(dir, units); err != nil {
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
