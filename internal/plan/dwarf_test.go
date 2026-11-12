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
				"TERM=dumb /tc/bin/gcc -g -o $WORK/b001/native.o -c ./native.c\n" +
				"/go/compile -p main -buildid=generated/generated -o $WORK/b001/_pkg_.a ./main.go\n" +
				"GOROOT='' /go/link -buildid=generated " + tc.flags + " -o $WORK/b001/exe/a.out $WORK/b001/_pkg_.a\n"
			units, err := Parse(trace, "/source")
			if err != nil {
				t.Fatal(err)
			}
			if got := strings.Contains(units[0].Script, "/compile -dwarf=false -c=@CORES@"); got != tc.omit {
				t.Fatalf("omit DWARF = %v; want %v:\n%s", got, tc.omit, units[0].Script)
			}
			if got := strings.Contains(units[0].Script, " -g0 -c "); got != tc.omit {
				t.Fatalf("omit native DWARF = %v; want %v:\n%s", got, tc.omit, units[0].Script)
			}
			// Unknown SDKs keep their original compiler behavior even when
			// explicitly opted into the experimental trace adapter.
			driver, err := SelectDriver("go1.27.0", true)
			if err != nil {
				t.Fatal(err)
			}
			units, err = driver.Parse(trace, "/source")
			if err != nil || strings.Contains(units[0].Script, "-dwarf=false") || strings.Contains(units[0].Script, " -g0 ") {
				t.Fatalf("experimental compiler changed: %v, %v", units, err)
			}
		})
	}
}

func TestNativeDwarfCommandSelection(t *testing.T) {
	for _, tc := range []struct {
		command string
		omit    bool
	}{
		{"TERM=dumb /tc/bin/gcc -O2 -g -o out.o -c source.c", true},
		{"/tc/bin/g++ -ggdb3 -gdwarf-5 -o out.o -c source.cc", true},
		{"/tc/bin/clang -g -o out.o -c source.m", true},
		{"/tc/bin/clang++ -gno-record-gcc-switches -g -o out.o -c source.cc", true},
		{"/go/cgo -objdir work -- -O2 -g source.go", false},
		{"/tc/bin/gcc -g -c -x c - -o /dev/null || true", false},
		{"/tc/bin/gcc -g -o app first.o second.o", false},
		{"/tc/bin/gcc -I -c -o app object.o", false},
		{"/other/compiler -g -o out.o -c source.c", false},
		{"/tc/bin/gcc -g -- -c source.c", false},
		{"/tc/bin/gcc @flags -g -o out.o -c source.c", false},
		{"/tc/bin/gcc -g -gbtf -o out.o -c source.c", false},
		{"/tc/bin/gcc -g -gctf -o out.o -c source.c", false},
		{"/tc/bin/gcc -g -gsplit-dwarf -o out.o -c source.c", false},
		{"/tc/bin/clang -g -gcodeview -o out.o -c source.c", false},
		{"/tc/bin/clang -g -gfuture-format -o out.o -c source.c", false},
	} {
		t.Run(tc.command, func(t *testing.T) {
			got, err := quoteCommandWithDwarf(tc.command, true)
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(got, " -g0 -c ") != tc.omit {
				t.Fatalf("unexpected native debug setting: %s", got)
			}
			original, err := quoteCommand(tc.command)
			if err != nil || strings.Replace(got, " -g0 -c ", " -c ", 1) != original {
				t.Fatalf("changed other compiler arguments: %s; original %s (%v)", got, original, err)
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
