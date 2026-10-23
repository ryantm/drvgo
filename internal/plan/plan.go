// Package plan translates cmd/go's dry-run output into isolated compilation units.
// This intentionally supports a pinned Go version: -n is not a stable build-plan API.
package plan

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

type Unit struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Script  string   `json:"script"`
	Deps    []string `json:"deps"`
	Sources []string `json:"sources"`
	Binary  bool     `json:"binary"`
}

var start = regexp.MustCompile(`^mkdir -p \$WORK/(b[0-9]+)/$`)
var workRef = regexp.MustCompile(`\$WORK/(b[0-9]+)`)
var packageName = regexp.MustCompile(`(?:^|\n)#\n# ([^\n]+)\n#`)
var compileName = regexp.MustCompile(` -p ([^ ]+) `)
var cgoName = regexp.MustCompile(` -importpath ([^ ]+) `)

// Parse splits at the first creation of each action directory. Relative compiler
// source arguments are resolved against the working directory cmd/go elides.
func Parse(raw, cwd string) ([]Unit, error) {
	d, _ := SelectDriver("go1.26.5", false)
	return d.Parse(raw, cwd)
}

func parse(raw, cwd string, driver Driver) ([]Unit, error) {
	var units []Unit
	var script strings.Builder
	seen := map[string]bool{}
	current := -1
	directory := cwd
	heredoc := false
	for _, line := range strings.Split(raw, "\n") {
		if m := start.FindStringSubmatch(line); m != nil && !seen[m[1]] {
			if current >= 0 {
				units[current].Script = script.String()
			}
			script.Reset()
			script.WriteString("cd @WORK@\n")
			seen[m[1]] = true
			units = append(units, Unit{ID: m[1], Deps: []string{}, Sources: []string{}})
			current = len(units) - 1
		}
		if current < 0 {
			if strings.TrimSpace(line) == "" || strings.HasPrefix(line, "WORK=") || strings.HasPrefix(line, "go: downloading ") {
				continue
			}
			return nil, fmt.Errorf("unexpected dry-run preamble: %s", line)
		}
		if strings.HasPrefix(line, "cd ") {
			directory = strings.TrimPrefix(line, "cd ")
			if directory == "." {
				directory = cwd
			}
		}
		if strings.Contains(line, "/compile ") {
			line = strings.ReplaceAll(line, " ./", " "+directory+"/")
		}
		// cmd/go prints file bodies without adding a newline before EOF.
		if heredoc && strings.HasSuffix(line, "EOF") {
			if line != "EOF" {
				line = strings.TrimSuffix(line, "EOF") + "\nEOF"
			}
			heredoc = false
		}
		if strings.Contains(line, "<< 'EOF' # internal") {
			heredoc = true
		}
		// The final copy is performed by the Nix builder, not at the caller's path.
		if strings.HasPrefix(line, "mv ") && strings.HasSuffix(line, " /LIBNIX_OUTPUT") {
			continue
		}
		if line == "mkdir -p /" {
			continue
		}
		if strings.HasPrefix(line, "rm -rf $WORK/") {
			return nil, fmt.Errorf("dry run must use -work")
		}
		if !heredoc && (strings.HasPrefix(line, "/") || strings.HasPrefix(line, "TERM=") || strings.HasPrefix(line, "GOROOT=") || strings.HasPrefix(line, "go tool ")) {
			var err error
			line, err = driver.quoteCommand(line)
			if err != nil {
				return nil, fmt.Errorf("tool command: %w", err)
			}
		}
		script.WriteString(line)
		script.WriteByte('\n')
	}
	if current >= 0 {
		units[current].Script = script.String()
	}
	if len(units) == 0 {
		return nil, fmt.Errorf("no compilation actions in dry run")
	}
	for i := range units {
		u := &units[i]
		if m := packageName.FindStringSubmatch(u.Script); m != nil {
			u.Name = m[1]
		}
		if m := cgoName.FindStringSubmatch(u.Script); m != nil {
			u.Name = m[1]
		}
		if u.Name == "" {
			if m := compileName.FindStringSubmatch(u.Script); m != nil {
				u.Name = m[1]
			}
		}
		if u.Name == "" {
			return nil, fmt.Errorf("cannot identify package for %s", u.ID)
		}
		u.Binary = strings.Contains(u.Script, "/exe/a.out")
		deps := map[string]bool{}
		u.Script = workRef.ReplaceAllStringFunc(u.Script, func(s string) string {
			id := strings.TrimPrefix(s, "$WORK/")
			if id == u.ID {
				return "@SELF@"
			}
			deps[id] = true
			return "@DEP:" + id + "@"
		})
		u.Script = strings.ReplaceAll(u.Script, "$WORK", "@WORK@")
		u.Script = strings.ReplaceAll(u.Script, "go tool buildid ", "libnix-buildid ")
		u.Script = strings.ReplaceAll(u.Script, "go tool pack ", "libnix-pack ")
		var err error
		u.Script, err = driver.normalizeCgo(u.Script)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", u.Name, err)
		}
		for d := range deps {
			if !seen[d] {
				return nil, fmt.Errorf("%s references unknown action %s", u.ID, d)
			}
			u.Deps = append(u.Deps, d)
		}
		sort.Strings(u.Deps)
	}
	// A dry-run action graph must be acyclic before it is emitted as recursive Nix.
	byID := map[string]Unit{}
	for _, u := range units {
		byID[u.ID] = u
	}
	state := map[string]int{}
	var visit func(string) error
	visit = func(id string) error {
		if state[id] == 1 {
			return fmt.Errorf("cycle at %s", id)
		}
		if state[id] == 2 {
			return nil
		}
		state[id] = 1
		for _, d := range byID[id].Deps {
			if err := visit(d); err != nil {
				return err
			}
		}
		state[id] = 2
		return nil
	}
	for _, u := range units {
		if err := visit(u.ID); err != nil {
			return nil, err
		}
	}
	return units, nil
}
