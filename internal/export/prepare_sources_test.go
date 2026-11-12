package export

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/ryantm/drvgo/internal/plan"
)

func TestPrepareSourcesParallelDeterminism(t *testing.T) {
	dir := t.TempDir()
	const count = 24
	var units []plan.Unit
	for i := range count {
		id := fmt.Sprintf("snapshot-%02d", i)
		root := filepath.Join(dir, "sources", id)
		if err := os.MkdirAll(filepath.Join(root, "nested"), 0755); err != nil {
			t.Fatal(err)
		}
		data := strings.Repeat(fmt.Sprintf("source %d\n", i), 700)
		if i%3 == 0 {
			data = strings.Repeat(data, 20)
		}
		if err := os.WriteFile(filepath.Join(root, "nested", "source"), []byte(data), 0644); err != nil {
			t.Fatal(err)
		}
		units = append(units, plan.Unit{ID: id, Sources: []string{id}, Script: "compile @SRC:" + id + "@/nested/source\n"})
	}
	// Reused snapshots must keep the caller's source order and script context.
	units = append(units, plan.Unit{Sources: []string{"snapshot-23", "snapshot-00", "snapshot-01"}, Script: "link @SRC:snapshot-23@/nested/source @SRC:snapshot-00@/nested/source @SRC:snapshot-01@/nested/source\n"})
	clone := func() []plan.Unit {
		result := slices.Clone(units)
		for i := range result {
			result[i].Sources = slices.Clone(result[i].Sources)
		}
		return result
	}
	previous := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(previous)
	want := clone()
	if err := prepareSources(dir, want); err != nil {
		t.Fatal(err)
	}
	for i := range count {
		if (len(want[i].Sources) > 0) != (i%3 == 0) {
			t.Fatalf("wrong inlining selection for %d", i)
		}
	}
	runtime.GOMAXPROCS(4)
	for range 3 {
		got := clone()
		if err := prepareSources(dir, got); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatal("parallel preparation changed scripts or source order")
		}
	}
	// A missing selected snapshot cannot partially rewrite the unit graph.
	failed := append(clone(), plan.Unit{Sources: []string{"missing"}, Script: "compile @SRC:missing@/source\n"})
	original := append(clone(), failed[len(failed)-1])
	if err := prepareSources(dir, failed); err == nil {
		t.Fatal("missing source accepted")
	}
	if !reflect.DeepEqual(failed, original) {
		t.Fatal("failed source preparation changed the graph")
	}
}
