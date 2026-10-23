package plan

import (
	"fmt"
	"regexp"
	"strings"
)

var dynPackage = regexp.MustCompile(` -dynpackage ([a-zA-Z_][a-zA-Z0-9_]*) `)

// normalizeCgo restores the branch cmd/go cannot take in -n mode. A package's
// preliminary native link can legitimately fail when another package provides
// its C symbols. Go then packs a dynimportfail marker to request external linking.
// See cmd/go/internal/work/exec.go: Builder.dynimport.
func normalizeCgo(script string) (string, error) {
	var out strings.Builder
	hasDynImport := strings.Contains(script, " -dynimport ")
	for _, line := range strings.Split(script, "\n") {
		if strings.HasSuffix(line, " -o /dev/null || true") {
			continue
		} // compiler capability probes
		if strings.Contains(line, " # test for internal linking errors (") {
			continue
		} // diagnostic duplicate
		if hasDynImport && strings.Contains(line, " -o @SELF@/_cgo_.o ") {
			line = "if ! " + line + " >@SELF@/dynlink.log 2>&1; then\n  touch @SELF@/dynimportfail\nfi"
		} else if strings.Contains(line, " -dynimport ") {
			m := dynPackage.FindStringSubmatch(line)
			if m == nil {
				return "", fmt.Errorf("unrecognized cgo dynimport command")
			}
			line = "if test -f @SELF@/dynimportfail; then\n  printf 'package " + m[1] + "\\n' >@SELF@/_cgo_import.go\nelse\n" + line + "\nfi"
		} else if hasDynImport && strings.HasPrefix(line, "libnix-buildid -w ") {
			line = "if test -f @SELF@/dynimportfail; then\n  libnix-pack r @SELF@/_pkg_.a @SELF@/dynimportfail\nfi\n" + line
		}
		out.WriteString(line)
		out.WriteByte('\n')
	}
	return out.String(), nil
}
