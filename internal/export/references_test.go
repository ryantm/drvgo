package export

import (
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
	if err := os.WriteFile(mock, []byte("#!/bin/sh\nprintf '%s\\n' '{\""+valid+"\":{},\""+missing+"\":null}'\nexit 1\n"), 0755); err != nil {
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
	mock := filepath.Join(t.TempDir(), "nix")
	if err := os.WriteFile(mock, []byte("#!/bin/sh\nprintf '{}'\nexit 1\n"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", filepath.Dir(mock))
	units := []plan.Unit{{Script: "/nix/store/" + strings.Repeat("1", 32) + "-runtime-data"}}
	if err := collectStoreReferences(t.TempDir(), units); err == nil {
		t.Fatal("silently dropped a reference on store query failure")
	}
}
