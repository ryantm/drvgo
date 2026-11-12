package plan

import "strings"

// InternalLink reports whether the single root link explicitly uses Go's
// internal linker and the compiler whose runtime queries our bundle preserves.
// Automatic mode can still require external linking, so it stays unchanged.
func (d Driver) InternalLink(raw, compiler string) bool {
	if d.Experimental {
		return false
	}
	args, ok := traceLinkArgs(strings.Split(raw, "\n"))
	if !ok {
		return false
	}
	mode, extld := "", ""
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if !strings.HasPrefix(arg, "-") || arg == "-" || arg == "--" {
			break
		}
		name, value, inline := strings.Cut(strings.TrimPrefix(strings.TrimPrefix(arg, "-"), "-"), "=")
		if !inline && (name == "buildid" || linkerFlagTakesValue(name)) {
			i++
			if i == len(args) {
				return false
			}
			value = args[i]
		}
		switch name {
		case "linkmode":
			mode = value
		case "extld":
			extld = value
		}
	}
	return mode == "internal" && extld == compiler
}

// File bodies are data. A trace with multiple or malformed link actions is
// ineligible for optimizations based on a single root link's flags.
func traceLinkArgs(lines []string) ([]string, bool) {
	heredoc := false
	var args []string
	links := 0
	for _, line := range lines {
		if heredoc {
			if strings.HasSuffix(line, "EOF") {
				heredoc = false
			}
			continue
		}
		if strings.Contains(line, "<< 'EOF' # internal") {
			heredoc = true
			continue
		}
		if !strings.Contains(line, "/link") {
			continue
		}
		words, err := commandWords(line)
		if err != nil {
			return nil, false
		}
		for i, tool := range words {
			if isAssignment(tool) {
				continue
			}
			if strings.HasSuffix(tool, "/link") {
				links++
				args = words[i+1:]
			}
			break
		}
	}
	return args, links == 1 && !heredoc
}
