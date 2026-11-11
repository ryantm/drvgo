package export

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/ryantm/drvgo/internal/plan"
)

func TestStoreReferencesSurviveInlining(t *testing.T) {
	valid := "/nix/store/" + strings.Repeat("1", 32) + "-runtime-data"
	missing := "/nix/store/" + strings.Repeat("0", 32) + "-missing"
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "sources", "small"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sources", "small", "main.go"),
		[]byte(valid+"/file "+missing), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "sources", "unselected"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sources", "unselected", "asset"),
		[]byte("/nix/store/"+strings.Repeat("2", 32)+"-unselected"), 0644); err != nil {
		t.Fatal(err)
	}
	mock := filepath.Join(t.TempDir(), "nix")
	if err := os.WriteFile(mock, []byte("#!/bin/sh\nprintf '%s\\n' '{\""+valid+"\":{},\""+missing+"\":null}'\n"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", filepath.Dir(mock))
	units := []plan.Unit{
		{Sources: []string{"small"}, Script: "compile @SRC:small@/main.go"},
		{Script: "link -X data=" + valid + "/file"},
		{Script: "compile unrelated"},
	}
	if err := collectStoreReferences(dir, units); err != nil {
		t.Fatal(err)
	}
	if err := inlineSources(dir, units); err != nil {
		t.Fatal(err)
	}
	for _, unit := range units[:2] {
		if !reflect.DeepEqual(unit.StoreReferences, []string{valid}) {
			t.Fatalf("references: %v", unit.StoreReferences)
		}
	}
	if len(units[2].StoreReferences) != 0 {
		t.Fatal("references leaked into an unrelated unit")
	}
	if len(units[0].Sources) != 0 || strings.Contains(units[0].Script, valid) {
		t.Fatal("test did not exercise encoded source")
	}
}

func TestStoreReferenceQueryFailsClosed(t *testing.T) {
	path := "/nix/store/" + strings.Repeat("1", 32) + "-runtime-data"
	for _, tc := range []struct{ name, output, status, want string }{
		{"failed", fmt.Sprintf(`{"%s":{}}`, path), "echo unavailable >&2; exit 1", "unavailable"},
		{"omitted", `{}`, "", "omitted"},
		{"invalid-json", `{`, "", "decode"},
		{"invalid-value", fmt.Sprintf(`{"%s":true}`, path), "", "decode"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			mock := filepath.Join(t.TempDir(), "nix")
			if err := os.WriteFile(mock, []byte("#!/bin/sh\nprintf '%s' '"+tc.output+"'\n"+tc.status+"\n"), 0755); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", filepath.Dir(mock))
			units := []plan.Unit{{Script: path}}
			if err := collectStoreReferences(t.TempDir(), units); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected %q error, got %v", tc.want, err)
			}
		})
	}
}

func TestStoreReferenceQueryBatches(t *testing.T) {
	// More candidates than a single exec's argument limit on Linux or macOS.
	const count = 40000
	var script strings.Builder
	for i := 0; i < count; i++ {
		fmt.Fprintf(&script, "/nix/store/%s-data-%05d\n", strings.Repeat("1", 32), i)
	}
	mock := filepath.Join(t.TempDir(), "nix")
	if err := os.WriteFile(mock, []byte(`#!/bin/sh
[ "$#" -le 262 ] || exit 2
printf '{'
sep=''
for path in "$@"; do
  case "$path" in /nix/store/*) printf '%s"%s":{}' "$sep" "$path"; sep=',' ;; esac
done
printf '}'
`), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", filepath.Dir(mock))
	units := []plan.Unit{{Script: script.String()}}
	if err := collectStoreReferences(t.TempDir(), units); err != nil {
		t.Fatal(err)
	}
	if len(units[0].StoreReferences) != count {
		t.Fatalf("got %d references, want %d", len(units[0].StoreReferences), count)
	}
}

func TestScanStoreReferences(t *testing.T) {
	path := "/nix/store/" + strings.Repeat("1", 32) + "-" + strings.Repeat("a", 211)
	// Exercise every possible split through the prefix, hash, name and delimiter.
	for split := 1; split <= len(path); split++ {
		input := strings.Repeat(" ", 512-split) + path + "/file " + path + "x " + path
		found := map[string]bool{}
		err := scanStoreReferences(strings.NewReader(input), make([]byte, 512), func(p string) { found[p] = true })
		if err != nil || !reflect.DeepEqual(found, map[string]bool{path: true}) {
			t.Fatalf("split %d: %v, %v", split, found, err)
		}
	}
	want := errors.New("read failed")
	if err := scanStoreReferences(io.MultiReader(strings.NewReader(path), failingReferenceReader{want}), make([]byte, 512), func(string) {}); !errors.Is(err, want) {
		t.Fatalf("lost read error: %v", err)
	}
}

type failingReferenceReader struct{ err error }

func (r failingReferenceReader) Read([]byte) (int, error) { return 0, r.err }
