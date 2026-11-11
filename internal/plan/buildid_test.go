package plan

import (
	"strings"
	"testing"
)

func TestCustomBuildIDReplay(t *testing.T) {
	for _, tc := range []struct {
		name, flags string
		custom      bool
	}{
		{"generated", "", false},
		{"redacted", "-buildid=redacted", true},
		{"empty", "-buildid=", true},
		{"spaced", `-buildid "custom ID"`, true},
		{"last wins", "-buildid=custom -buildid=generated", false},
		{"quoted other flag", `-X "main.value= -buildid=custom"`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := "WORK=/tmp/work\nmkdir -p $WORK/b001/\n#\n# example.com/main\n#\n" +
				"/go/link -buildid=generated " + tc.flags + " -o $WORK/b001/exe/a.out $WORK/b001/_pkg_.a\n" +
				"go tool buildid -w $WORK/b001/exe/a.out # internal\n"
			units, err := Parse(raw, "/src")
			if err != nil {
				t.Fatal(err)
			}
			if got := strings.Contains(units[0].Script, "libnix-buildid -w "); got == tc.custom {
				t.Fatalf("custom=%v, script=%s", tc.custom, units[0].Script)
			}
		})
	}
}
