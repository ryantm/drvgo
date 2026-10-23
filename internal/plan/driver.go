package plan

import (
	"fmt"
	"go/version"
	"regexp"
	"strings"
)

// Driver isolates cmd/go's private trace dialect from the shared package graph.
// Keep the allowlist tied to the versions exercised by tests/compatibility.py.
type Driver struct {
	Version      string
	Family       string
	Experimental bool
}

var testedVersions = map[string]string{
	"go1.25.12": "cmd-go-1.25",
	"go1.26.5":  "cmd-go-1.26",
	"go1.26.6":  "cmd-go-1.26",
}

func SelectDriver(v string, experimental bool) (Driver, error) {
	if family, ok := testedVersions[v]; ok {
		return Driver{v, family, false}, nil
	}
	if !version.IsValid(v) || version.Compare(v, "go1.23.0") < 0 {
		return Driver{}, fmt.Errorf("unsupported Go version %q: need a numbered Go release >= 1.23", v)
	}
	if !experimental {
		return Driver{}, fmt.Errorf("untested dry-run driver %s; tested versions: go1.25.12, go1.26.5, go1.26.6; select a tested toolchain or explicitly use -experimental-go", v)
	}
	return Driver{v, "cmd-go-experimental", true}, nil
}

func (d Driver) Parse(raw, cwd string) ([]Unit, error) {
	if d.Family == "cmd-go-1.25" || d.Experimental {
		raw = legacyActionHeader.ReplaceAllString(raw, "$2\n#\n# $1\n#\n")
	}
	return parse(raw, cwd, d)
}

// Quoting and cgo replay currently share implementations across these families.
// Add version-specific behavior here when recorded traces require it.
func (d Driver) quoteCommand(line string) (string, error)   { return quoteCommand(line) }
func (d Driver) normalizeCgo(script string) (string, error) { return normalizeCgo(script) }
func (d Driver) ListArgs() []string {
	fields := []string{"Dir", "ImportPath", "Name", "Target", "Standard", "DepOnly", "Module", "GoFiles", "CgoFiles", "CFiles", "CXXFiles", "MFiles", "HFiles", "FFiles", "SFiles", "SysoFiles", "EmbedFiles", "CgoPkgConfig", "SwigFiles", "SwigCXXFiles"}
	return []string{"list", "-deps", "-json=" + strings.Join(fields, ",")}
}

// Go 1.25 prints package headers before the first action mkdir; Go 1.26
// prints them after it. Move only this exact legacy boundary into the common
// dialect, so the header belongs to its own package rather than its predecessor.
var legacyActionHeader = regexp.MustCompile(`(?m)^#
# ([^
]+)
#

(mkdir -p \$WORK/b[0-9]+/
)`)
