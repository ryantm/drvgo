package plan

import (
	"strconv"
	"strings"
)

// cmd/go ends native object compilations with "-c source". Only rewrite that
// shape for its supported compiler drivers; cgo's own DWARF type probes,
// compiler capability checks and native link commands must stay unchanged.
func omitNativeDwarf(words []string, command int) []string {
	tool := words[command]
	if !(strings.HasSuffix(tool, "/gcc") || strings.HasSuffix(tool, "/g++") ||
		strings.HasSuffix(tool, "/clang") || strings.HasSuffix(tool, "/clang++")) ||
		len(words) <= command+2 || words[len(words)-2] != "-c" {
		return words
	}
	for _, arg := range words[command+1:] {
		// Response files and an option terminator make the apparent suffix
		// insufficient to establish the actual compiler options.
		if arg == "--" || strings.HasPrefix(arg, "@") {
			return words
		}
		if strings.HasPrefix(arg, "-g") {
			switch arg {
			case "-g", "-g0", "-g1", "-g2", "-g3",
				"-ggdb", "-ggdb0", "-ggdb1", "-ggdb2", "-ggdb3",
				"-gdwarf", "-gdwarf-2", "-gdwarf-3", "-gdwarf-4", "-gdwarf-5",
				"-gno-record-gcc-switches":
			default:
				// -g0 can also remove explicitly requested formats such as
				// BTF. Preserve custom debug formats and unknown settings.
				return words
			}
		}
	}
	end := len(words) - 2
	args := append([]string{}, words[:end]...)
	args = append(args, "-g0")
	return append(args, words[end:]...)
}

// Only optimize a single link action that explicitly discards DWARF. File
// bodies are data, and flag operands can themselves contain apparent flags.
// Unsupported or malformed commands retain the original compiler behavior.
func traceOmitsDwarf(lines []string) bool {
	args, ok := traceLinkArgs(lines)
	return ok && linkOmitsDwarf(args)
}

func linkOmitsDwarf(args []string) bool {
	strip, dwarf, explicitDwarf := false, false, false
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if !strings.HasPrefix(arg, "-") || arg == "-" || arg == "--" {
			break
		}
		name, value, inline := strings.Cut(strings.TrimPrefix(strings.TrimPrefix(arg, "-"), "-"), "=")
		if name == "s" || name == "w" {
			if !inline {
				value = "true"
			}
			v, err := strconv.ParseBool(value)
			if err != nil {
				return false
			}
			if name == "s" {
				strip = v
			} else {
				dwarf, explicitDwarf = v, true
			}
		} else if !inline && (name == "buildid" || linkerFlagTakesValue(name)) {
			i++
			if i == len(args) {
				return false
			}
		}
	}
	// cmd/link treats -w as a ternary flag: -s implies -w only when -w
	// was never set. In particular, -s -w=false must keep compiler DWARF.
	if explicitDwarf {
		return dwarf
	}
	return strip
}
