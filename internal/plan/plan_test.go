package plan

import (
	"fmt"
	"strings"
	"testing"
)

func TestCompilerConcurrency(t *testing.T) {
	var previous string
	for _, cores := range []int{1, 2, 20} {
		flag := ""
		if cores > 1 {
			flag = fmt.Sprintf(" -c=%d", cores)
		}
		trace := fmt.Sprintf("WORK=/tmp/go\nmkdir -p $WORK/b001/\n#\n# example/main\n#\n/tool/compile -o $WORK/b001/_pkg_.a -p main%s ./main.go\n", flag)
		units, err := Parse(trace, "/source")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(units[0].Script, "-c=@CORES@") {
			t.Fatalf("missing builder budget: %s", units[0].Script)
		}
		if previous != "" && previous != units[0].Script {
			t.Fatal("planner CPU count changed the recipe")
		}
		previous = units[0].Script
	}
}

func TestParseDependenciesAndEmbedConfig(t *testing.T) {
	raw := `mkdir -p $WORK/b002/
#
# example/dep
#
cd /project
/tool/compile -p example/dep -o $WORK/b002/_pkg_.a ./dep/dep.go
mkdir -p $WORK/b001/
#
# example/main
#
cat >$WORK/b001/importcfg << 'EOF' # internal
packagefile example/dep=$WORK/b002/_pkg_.a
EOF
cat >$WORK/b001/embedcfg << 'EOF' # internal
{"Files":{"data":"/project/data"}}EOF
/tool/compile -p main -o $WORK/b001/_pkg_.a ./main.go
/tool/link -o $WORK/b001/exe/a.out $WORK/b001/_pkg_.a
mv $WORK/b001/exe/a.out /LIBNIX_OUTPUT
`
	units, err := Parse(raw, "/project")
	if err != nil {
		t.Fatal(err)
	}
	if len(units) != 2 || len(units[1].Deps) != 1 || units[1].Deps[0] != "b002" || !units[1].Binary {
		t.Fatalf("bad graph: %+v", units)
	}
	s := units[1].Script
	for _, want := range []string{"@DEP:b002@/_pkg_.a", "/project/main.go", "}}\nEOF"} {
		if !strings.Contains(s, want) {
			t.Fatalf("missing %q in %s", want, s)
		}
	}
	if strings.Contains(s, "$WORK") || strings.Contains(s, "mv ") {
		t.Fatalf("untranslated script: %s", s)
	}
}

func TestRejectMalformedPlans(t *testing.T) {
	for name, raw := range map[string]string{
		"empty": "", "unexpected": "fatal error", "unknown dependency": "mkdir -p $WORK/b001/\n/tool/compile -p main $WORK/b999/_pkg_.a\n",
		"cycle": "mkdir -p $WORK/b001/\n/tool/compile -p main $WORK/b002/_pkg_.a\nmkdir -p $WORK/b002/\n/tool/compile -p dep $WORK/b001/_pkg_.a\n",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := Parse(raw, "/project"); err == nil {
				t.Fatal("accepted invalid plan")
			}
		})
	}
}
