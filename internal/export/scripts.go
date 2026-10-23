package export

import (
	"strings"

	"github.com/ryantm/drvgo/internal/plan"
)

// Nix double-quoted strings have different escaping rules from JSON/Go strings.
var nixEscapes = strings.NewReplacer(`\`, `\\`, `"`, `\"`, "\n", `\n`, "\r", `\r`, "\t", `\t`, "${", `\${`)

func nixString(s string) string { return `"` + nixEscapes.Replace(s) + `"` }

// Render interpolation once in Go instead of repeatedly searching long replay
// scripts during Nix evaluation. Only declared tokens are substituted; literal
// shell ${...} expressions and unknown token-like filenames stay literal.
func nixScripts(units []plan.Unit) string {
	var out strings.Builder
	out.WriteString("# Generated replay scripts; regenerate with drvgo export.\nunits: sources: {\n")
	for _, u := range units {
		var pairs []string
		for _, id := range u.Deps {
			pairs = append(pairs, "@DEP:"+id+"@", "${units."+nixString(id)+"}")
		}
		for _, id := range u.Sources {
			pairs = append(pairs, "@SRC:"+id+"@", "${sources."+nixString(id)+"}")
		}
		script := strings.NewReplacer(pairs...).Replace(nixString(u.Script))
		out.WriteString(nixString(u.ID))
		out.WriteString(" = ")
		out.WriteString(script)
		out.WriteString(";\n")
	}
	out.WriteString("}\n")
	return out.String()
}
