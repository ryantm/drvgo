package plan

import (
	"os/exec"
	"strings"
	"testing"
)

func TestQuoteCommandLiteralArguments(t *testing.T) {
	line, err := quoteCommand(`/usr/bin/printf "%s\\n" "literal $HOME $(false) ` + "`false`" + `" "single'quote" "a\tb"`)
	if err != nil {
		t.Fatal(err)
	}
	line = strings.Replace(line, "/usr/bin/printf", "printf", 1)
	out, err := exec.Command("bash", "-c", line).CombinedOutput()
	if err != nil {
		t.Fatalf("%s: %v\n%s", line, err, out)
	}
	want := "literal $HOME $(false) `false`\nsingle'quote\na\tb\n"
	if string(out) != want {
		t.Fatalf("got %q want %q; %s", out, want, line)
	}
}

func TestCompilerBudgetPreservesQuotedFilenames(t *testing.T) {
	got, err := quoteCommand(`TERM='dumb' /tool/compile -c=20 -o /tmp/pkg.a "/source/file -c=20 name.go"`)
	if err != nil {
		t.Fatal(err)
	}
	want := "TERM=dumb /tool/compile -c=@CORES@ -o /tmp/pkg.a '/source/file -c=20 name.go'"
	if got != want {
		t.Fatalf("got %q; want %q", got, want)
	}
}
func TestCgoFallback(t *testing.T) {
	script := `TERM='dumb' gcc -o @SELF@/_cgo_.o @SELF@/_x001.o
gcc -o @SELF@/_cgo_.o @SELF@/_x001.o # test for internal linking errors (succeeded)
TERM='dumb' /tool/cgo -dynpackage parser -dynimport @SELF@/_cgo_.o -dynout @SELF@/_cgo_import.go
libnix-buildid -w @SELF@/_pkg_.a
`
	out, err := normalizeCgo(script)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"if ! TERM=", "printf 'package parser\\n'", "libnix-pack r @SELF@/_pkg_.a @SELF@/dynimportfail"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q: %s", want, out)
		}
	}
	if strings.Contains(out, "# test for internal linking") {
		t.Fatal("replayed diagnostic duplicate")
	}
}
