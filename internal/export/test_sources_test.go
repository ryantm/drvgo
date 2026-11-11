package export

import (
	"strings"
	"testing"
)

func TestPrepareTestSources(t *testing.T) {
	target := Package{Dir: "/project/p", ImportPath: "example/p.test", Name: "main", Standard: true, GoFiles: []string{"/cache/generated-main"}}
	packages := []Package{
		{Dir: "/project/p", ImportPath: "example/p", GoFiles: []string{"p.go"}},
		{Dir: "/project/p", ImportPath: "example/p [example/p.test]", GoFiles: []string{"p.go", "p_test.go"}, EmbedFiles: []string{"fixture.txt"}},
		{Dir: "/project/p", ImportPath: "example/p_test [example/p.test]", GoFiles: []string{"external_test.go"}},
		target,
	}
	trace := []byte("mkdir -p $WORK/b001/\n#\n# example/p.test\n#\ncompile $WORK/b001/_testmain.go\nlink -o $WORK/b001/p.test\n")
	got, raw, err := prepareTestSources(packages, target, trace)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || strings.Join(got[0].GoFiles, ",") != "p.go,p_test.go,external_test.go" {
		t.Fatalf("merged sources: %#v", got)
	}
	if len(got[0].EmbedFiles) != 1 || got[1].Dir != "/cache" || got[1].Standard {
		t.Fatalf("missing generated or embedded sources: %#v", got)
	}
	for _, want := range []string{"compile /cache/generated-main", "link -o $WORK/b001/exe/a.out"} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("missing %q in %s", want, raw)
		}
	}
}

func TestTestMainSelection(t *testing.T) {
	for _, name := range []string{"value", "main"} {
		if isTestMain(Package{Name: name, ImportPath: "example/value.test", GoFiles: []string{"main.go"}}) {
			t.Fatal("ordinary package mistaken for generated test main")
		}
	}
	if !isTestMain(Package{Name: "main", ImportPath: "example/value.test.test", GoFiles: []string{"/cache/generated-main"}}) {
		t.Fatal("generated test main not selected")
	}
}

func TestRaceRejectsDisabledCGOBeforeDiscovery(t *testing.T) {
	cgo := false
	_, err := Generate(Options{Race: true, CGOEnabled: &cgo, Toolchain: "/nonexistent"})
	if err == nil || !strings.Contains(err.Error(), "-race requires cgo") {
		t.Fatalf("incompatible build modes: %v", err)
	}
}

func TestPrepareTestSourcesRejectsMissingGeneratedMain(t *testing.T) {
	_, _, err := prepareTestSources(nil, Package{}, nil)
	if err == nil {
		t.Fatal("accepted missing generated test main")
	}
}
