package export

import (
	"fmt"
	"go/version"
	"os"
	"path/filepath"
	"strings"
)

// Read only version directives; cmd/go remains responsible for full syntax,
// dependency and workspace validation. controlledEnv always uses workspace auto
// discovery and disables automatic toolchain switching.
func checkRequirements(dir, selected string) ([]string, error) {
	find := func(name string) (string, error) {
		for p := dir; ; p = filepath.Dir(p) {
			path := filepath.Join(p, name)
			if _, err := os.Stat(path); err == nil {
				return path, nil
			} else if !os.IsNotExist(err) {
				return "", err
			}
			if filepath.Dir(p) == p {
				return "", nil
			}
		}
	}
	mod, err := find("go.mod")
	if err != nil {
		return nil, err
	}
	work, err := find("go.work")
	if err != nil {
		return nil, err
	}
	var diagnostics []string
	for _, path := range []string{mod, work} {
		if path == "" {
			continue
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		for _, line := range strings.Split(string(b), "\n") {
			line, _, _ = strings.Cut(line, "//")
			fields := strings.Fields(line)
			if len(fields) != 2 {
				continue
			}
			directive, value := fields[0], fields[1]
			if directive == "go" && version.IsValid("go"+value) && version.Compare(selected, "go"+value) < 0 {
				return nil, fmt.Errorf("%s requires Go >= %s, but selected toolchain is %s (GOTOOLCHAIN=local); select a newer tested compiler with -toolchain or mkGoPackage.go", path, value, selected)
			}
			if directive == "toolchain" && (work == "" || path == work) && version.IsValid(value) && version.Compare(selected, value) < 0 {
				diagnostics = append(diagnostics, fmt.Sprintf("%s suggests %s; keeping explicitly selected %s with GOTOOLCHAIN=local", path, value, selected))
			}
		}
	}
	return diagnostics, nil
}
