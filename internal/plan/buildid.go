package plan

import (
	"fmt"
	"strings"
)

type buildIDFlag struct {
	start, end int
	value      string
}

// Match cmd/compile and cmd/link's flag grammar, including operands that look
// like flags. The compiler's user flags are not exposed by the export API.
func buildIDFlags(words []string, command int, kind string) ([]buildIDFlag, error) {
	var flags []buildIDFlag
	for i := command + 1; i < len(words); i++ {
		arg := words[i]
		if !strings.HasPrefix(arg, "-") || arg == "-" || arg == "--" {
			break
		}
		name, value, inline := strings.Cut(strings.TrimPrefix(strings.TrimPrefix(arg, "-"), "-"), "=")
		takesValue := linkerFlagTakesValue(name)
		if kind == "compile" {
			takesValue = compilerFlagTakesValue(name)
		}
		start := i
		if !inline && (name == "buildid" || takesValue) {
			if i+1 == len(words) {
				return nil, fmt.Errorf("%s flag %s has no operand", kind, arg)
			}
			i++
			value = words[i]
		}
		if name == "buildid" {
			flags = append(flags, buildIDFlag{start, i + 1, value})
		}
	}
	return flags, nil
}

func compilerFlagTakesValue(name string) bool {
	switch name {
	case "D", "I", "c", "d", "o", "p", "asmhdr", "bench", "blockprofile",
		"cpuprofile", "embedcfg", "env", "gendwarfinl", "goversion", "importcfg",
		"installsuffix", "json", "lang", "linkobj", "memprofile", "memprofilerate",
		"mutexprofile", "coveragecfg", "spectre", "symabis", "traceprofile",
		"trimpath", "pgoprofile":
		return true
	}
	return false
}

// The dry run's automatic IDs can contain the original checkout/module-cache
// location through cgo flags. Remove them from the Nix recipe. The native worker
// derives replacements from that recipe after Nix resolves its actual inputs.
func canonicalBuildIDs(words []string) ([]string, error) {
	for i, tool := range words {
		if isAssignment(tool) {
			continue
		}
		kind := ""
		if strings.HasSuffix(tool, "/compile") {
			kind = "compile"
		} else if strings.HasSuffix(tool, "/link") {
			kind = "link"
		} else {
			return words, nil
		}
		flags, err := buildIDFlags(words, i, kind)
		if err != nil || len(flags) == 0 {
			return words, err
		}
		custom := len(flags) > 1 && flags[0].value != flags[len(flags)-1].value
		result := append([]string{}, words[:i]...)
		if !custom {
			result = append(result, "drvgo-"+kind)
		} else {
			// Keep the explicit override verbatim, including empty IDs. Only
			// the first, overridden cmd/go ID is irrelevant to this recipe.
			flags = flags[:1]
		}
		start := i
		for _, flag := range flags {
			result = append(result, words[start:flag.start]...)
			start = flag.end
		}
		return append(result, words[start:]...), nil
	}
	return words, nil
}
