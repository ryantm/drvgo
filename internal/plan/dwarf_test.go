package plan

import (
	"strings"
	"testing"
)

func TestStrippedLinkCompilerDwarf(t *testing.T) {
	for _, tc := range []struct {
		flags string
		omit  bool
	}{
		{"", false}, {"-s", true}, {"-w", true}, {"-s -w", true},
		{"--s=true --w=true", true}, {"-w=1", true}, {"-w=FALSE", false},
		{"-s -w=false", false}, {"-w=false -s", false},
		{"-s=false -w", true}, {"-s -s=false", false},
		{"-w=false -w=true", true}, {"-w=true -w=0", false},
		{`-X "main.value= -w -s"`, false}, {"-r -w", false},
		{"-r -s -w=false", false}, {"-buildid -w", false},
		{"-extldflags -s", false}, {"-extldflags=-w", false},
		{"-s -X main.value=-w=false", true}, {"-w=invalid", false},
	} {
		t.Run(tc.flags, func(t *testing.T) {
			trace := "mkdir -p $WORK/b001/\n" +
				"/go/compile -p main -buildid=generated/generated -o $WORK/b001/_pkg_.a ./main.go\n" +
				"GOROOT='' /go/link -buildid=generated " + tc.flags + " -o $WORK/b001/exe/a.out $WORK/b001/_pkg_.a\n"
			units, err := Parse(trace, "/source")
			if err != nil {
				t.Fatal(err)
			}
			if got := strings.Contains(units[0].Script, "/compile -dwarf=false -c=@CORES@"); got != tc.omit {
				t.Fatalf("omit DWARF = %v; want %v:\n%s", got, tc.omit, units[0].Script)
			}
			// Unknown SDKs keep their original compiler behavior even when
			// explicitly opted into the experimental trace adapter.
			driver, err := SelectDriver("go1.27.0", true)
			if err != nil {
				t.Fatal(err)
			}
			units, err = driver.Parse(trace, "/source")
			if err != nil || strings.Contains(units[0].Script, "-dwarf=false") {
				t.Fatalf("experimental compiler changed: %v, %v", units, err)
			}
		})
	}
}

func TestDwarfDetectionSkipsFileBodiesAndRequiresOneLink(t *testing.T) {
	for _, tc := range []struct {
		trace string
		omit  bool
	}{
		{"cat >modinfo << 'EOF' # internal\n/go/link -w\nEOF\n/go/link main.a", false},
		{"cat >modinfo << 'EOF' # internal\n/go/link -w=falseEOF\n/go/link -w main.a", true},
		{"/go/link -w main.a\n/go/link -w other.a", false},
		{"/go/compile -D /go/link -w file.go", false},
		{"/go/link -w -X", false},
	} {
		if got := traceOmitsDwarf(strings.Split(tc.trace, "\n")); got != tc.omit {
			t.Errorf("omit DWARF = %v; want %v for %q", got, tc.omit, tc.trace)
		}
	}
}
